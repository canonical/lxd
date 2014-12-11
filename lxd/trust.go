package main

import (
	"bytes"
	"os"

	"github.com/lxc/lxd"
	"golang.org/x/crypto/scrypt"
)

func (d *Daemon) hasPwd() bool {
	passfname := lxd.VarPath("adminpwd")
	_, err := os.Open(passfname)
	return err == nil
}

func (d *Daemon) verifyAdminPwd(password string) bool {
	passfname := lxd.VarPath("adminpwd")
	passOut, err := os.Open(passfname)
	if err != nil {
		lxd.Debugf("verifyAdminPwd: no password is set")
		return false
	}
	defer passOut.Close()
	buff := make([]byte, PW_SALT_BYTES+PW_HASH_BYTES)
	_, err = passOut.Read(buff)
	if err != nil {
		lxd.Debugf("failed to read the saved admin pasword for verification")
		return false
	}
	salt := buff[0:PW_SALT_BYTES]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
	if err != nil {
		lxd.Debugf("failed to create hash to check")
		return false
	}
	if !bytes.Equal(hash, buff[PW_SALT_BYTES:]) {
		lxd.Debugf("Bad password received")
		return false
	}
	lxd.Debugf("Verified the admin password")
	return true
}
