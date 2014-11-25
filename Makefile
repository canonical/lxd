.PHONY: default
default:
	make -C lxc
	make -C lxd

.PHONY: check
check: default
	go fmt ./...
	go vet ./...
	git diff --exit-code
	cd test && ./main.sh

.PHONY: clean
clean:
	-rm -f lxc/lxc lxd/lxd
