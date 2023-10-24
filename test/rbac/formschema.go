package main

import (
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/form"
	"gopkg.in/juju/environschema.v1"
)

var schemaResponse = form.SchemaResponse{
	Schema: schemaFields,
}

var schemaFields = environschema.Fields{
	"username": environschema.Attr{
		Description: "username",
		Type:        environschema.Tstring,
		Mandatory:   true,
	},
	"password": environschema.Attr{
		Description: "password",
		Type:        environschema.Tstring,
		Mandatory:   true,
		Secret:      true,
	},
}
