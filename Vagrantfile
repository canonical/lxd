Vagrant.configure('2') do |config|
    # grab Ubuntu 14.04 boxcutter image: https://atlas.hashicorp.com/boxcutter
    config.vm.box = "boxcutter/ubuntu1404" # Ubuntu 14.04

    # fix issues with slow dns https://www.virtualbox.org/ticket/13002
    config.vm.provider :virtualbox do |vb, override|
        vb.customize ["modifyvm", :id, "--natdnsproxy1", "off"]
    end

    config.vm.network "forwarded_port", guest: 443, host: 8443
    config.vm.provision :shell, :privileged => false, :path => "scripts/vagrant/install-go.sh"
    config.vm.provision :shell, :privileged => false, :path => "scripts/vagrant/install-lxd.sh"

end
