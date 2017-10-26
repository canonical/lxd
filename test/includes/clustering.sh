# Test helper for clustering

setup_clustering_bridge() {
  name="br$$"

  echo "==> Setup clustering bridge ${name}"

  brctl addbr "${name}"
  ip addr add 10.1.1.1/16 dev "${name}"
  ip link set dev "${name}" up

  iptables -t nat -A POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
  echo 1 > /proc/sys/net/ipv4/ip_forward
}

teardown_clustering_bridge() {
  name="br$$"

  if brctl show | grep -q "${name}" ; then
      echo "==> Teardown clustering bridge ${name}"
      echo 0 > /proc/sys/net/ipv4/ip_forward
      iptables -t nat -D POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
      ip link set dev "${name}" down
      ip addr del 10.1.1.1/16 dev "${name}"
      brctl delbr "${name}"
  fi
}

setup_clustering_netns() {
  id="${1}"
  shift

  prefix="lxd$$"
  ns="${prefix}${id}"

  echo "==> Setup clustering netns ${ns}"

  ip netns add "${ns}"

  veth1="v${ns}1"
  veth2="v${ns}2"

  ip link add "${veth1}" type veth peer name "${veth2}"
  ip link set "${veth2}" netns "${ns}"

  bridge="br$$"
  brctl addif "${bridge}" "${veth1}"

  ip link set "${veth1}" up

  ip netns exec "${ns}" ip link set dev lo up
  ip netns exec "${ns}" ip link set dev "${veth2}" name eth0
  ip netns exec "${ns}" ip link set eth0 up
  ip netns exec "${ns}" ip addr add "10.1.1.10${id}/16" dev eth0
  ip netns exec "${ns}" ip route add default via 10.1.1.1
}

teardown_clustering_netns() {
  prefix="lxd$$"
  bridge="br$$"
  for ns in $(ip netns | grep "${prefix}" | cut -f 1 -d " ") ; do
      echo "==> Teardown clustering netns ${ns}"
      veth1="v${ns}1"
      veth2="v${ns}2"
      ip netns exec "${ns}" ip link set eth0 down
      ip netns exec "${ns}" ip link set lo down
      ip link set "${veth1}" down
      brctl delif "${bridge}" "${veth1}"
      ip link delete "${veth1}" type veth
      ip netns delete "${ns}"
  done
}
