package main

import (
	"gopkg.in/juju/environschema.v1"
	"gopkg.in/macaroon-bakery.v2/httpbakery/form"
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
