.PHONY: default
default:
	make -C lxc
	make -C lxd

.PHONY: check
check: default
	test -z "$(shell go fmt ./...)"

.PHONY: clean
clean:
	-rm -f lxc/lxc lxc/lxd
