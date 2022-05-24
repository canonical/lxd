#!/bin/sh -e

# first run just to collect the name of the packages that are failing
for subdir in $(find . -type d); do
	if ! sh -ec "cd $subdir && golint -set_exit_status > /dev/null 2>&1"; then
		failing_packages="$subdir $failing_packages"
	fi
done

# now invoke golint passing the package names so that we get error reports with
# full path info
golint -set_exit_status $failing_packages
