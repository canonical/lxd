#!/bin/bash

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <instance> <project>"
    echo "Error: Both 'instance' and 'project' arguments are required."
    exit 1
fi

INSTANCE=$1
PROJECT=$2
SERVER_NAME="$(lxc info | awk '{if ($1 == "server_name:") print $2}')"
IS_LXD_CLUSTERED=$(lxc info | grep "server_clustered:" | grep "false")
if [ -z "$IS_LXD_CLUSTERED" ]; then
    echo "Error: LXD is clustered, this script only works for single node installations."
    echo "See https://documentation.ubuntu.com/lxd/latest/metrics/ for more information."
    exit 1
fi

CONTAINER_IP="$(lxc list "${INSTANCE}" --project "${PROJECT}" -c 4 -f csv | awk '{print $1}')"
CONTAINER_UPLINK_IP=$(echo "$CONTAINER_IP" | cut -d "." -f1,2,3)".1"
echo "Found container IP as '$CONTAINER_IP' and uplink as '$CONTAINER_UPLINK_IP'"

set -e
set -x

# upload server.crt to container
lxc info | sed -n "/BEGIN CERTIFICATE/,/END CERTIFICATE/ s/^\s*// p" | lxc file push --uid 0 --gid 0 --mode 0644 - "$INSTANCE"/root/server.crt --project="$PROJECT"

# install and configure grafana and prometheus in container
lxc exec "$INSTANCE" --project="$PROJECT" bash <<EOF
set -x
set -e
# install grafana and prometheus
apt-get update
apt-get install -y software-properties-common wget prometheus
mkdir -p /etc/apt/keyrings/
wget -q -O - https://apt.grafana.com/gpg.key | gpg --dearmor | tee /etc/apt/keyrings/grafana.gpg > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/grafana.gpg] https://apt.grafana.com stable main" | tee -a /etc/apt/sources.list.d/grafana.list
apt-get update
apt-get install -y grafana=11.6.0 loki=3.4.2 promtail=3.4.2
systemctl daemon-reload
systemctl enable --now grafana-server.service
systemctl enable --now loki
systemctl enable --now promtail

# generate ssl key for grafana to serve via https
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout /etc/grafana/grafana.key -out /etc/grafana/grafana.crt -days 365 -nodes -subj "/CN=metrics.local"
chown grafana:grafana /etc/grafana/grafana.crt
chown grafana:grafana /etc/grafana/grafana.key
chmod 0640 /etc/grafana/grafana.key /etc/grafana/grafana.crt
sed -i "s#;protocol = http#protocol = https#" /etc/grafana/grafana.ini
cat <<EOT > /etc/grafana/provisioning/datasources/lxd-sources.yaml
apiVersion: 1

datasources:
  - name: prometheus
    type: prometheus
    access: proxy
    url: http://$CONTAINER_IP:9090
  - name: loki
    type: loki
    access: proxy
    url: http://$CONTAINER_IP:3100
EOT
systemctl restart grafana-server

# generate certs for prometheus
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout metrics.key -nodes -out metrics.crt -days 3650 -subj "/CN=metrics.local"
mkdir /etc/prometheus/tls
mv metrics.* /etc/prometheus/tls/
mv server.crt /etc/prometheus/tls/
chown root:prometheus /etc/prometheus/tls/metrics.key
chmod 0640 /etc/prometheus/tls/metrics.key

# configure prometheus
cat <<EOT > /etc/prometheus/prometheus.yml
global:
  scrape_interval:     15s
  evaluation_interval: 15s
  scrape_timeout: 15s
scrape_configs:
  - job_name: lxd
    scrape_interval: 15s
    scrape_timeout: 15s
    metrics_path: '/1.0/metrics'
    scheme: 'https'
    static_configs:
      - targets: ['$CONTAINER_UPLINK_IP:8443']
    tls_config:
      ca_file: '/etc/prometheus/tls/server.crt'
      cert_file: '/etc/prometheus/tls/metrics.crt'
      key_file: '/etc/prometheus/tls/metrics.key'
      # XXX: server_name is required if the target name
      #      is not covered by the certificate (not in the SAN list)
      server_name: '$SERVER_NAME'
EOT
systemctl restart prometheus.service
EOF

# download metrics.crt from container and add to host lxd trust store
lxc file pull "$INSTANCE"/etc/prometheus/tls/metrics.crt --project="$PROJECT" /tmp/metrics.crt
lxc config trust add /tmp/metrics.crt --type=metrics
rm -rf /tmp/metrics.crt

# configure host lxd for loki and grafana
lxc config set user.ui_grafana_base_url=https://"$CONTAINER_IP":3000/d/bGY-LSB7k/lxd?orgId=1
lxc config set loki.api.url=http://"$CONTAINER_IP":3100 loki.instance=lxd &

# restart container
lxc restart "$INSTANCE" --project="$PROJECT"
sleep 10

set +x

# print grafana url
echo "Successfully initialized grafana"
echo "Next steps:"
echo "1. Wait for the container to finish booting"
echo "2. Sign in with admin/admin to grafana at https://$CONTAINER_IP:3000"
echo "3. Change password"
echo "4. Create a dashboard, see https://documentation.ubuntu.com/lxd/latest/howto/grafana/ for more details."
