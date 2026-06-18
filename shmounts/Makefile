CC=gcc
CFLAGS=-Wall
DESTDIR=/

default: setup-shmounts

setup-shmounts:
	$(CC) -o setup-shmounts setup-shmounts.c

install: setup-shmounts
	mkdir -p $(DESTDIR)/bin
	cp setup-shmounts $(DESTDIR)/bin/

clean:
	-rm -f setup-shmounts
