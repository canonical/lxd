package main

import (
	"github.com/juju/schema"

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

var fieldsChecker = schema.FieldMap(validateSchema(schemaFields))

func validateSchema(fields environschema.Fields) (schema.Fields, schema.Defaults) {
	f, d, err := fields.ValidationSchema()
	if err != nil {
		panic(err)
	}
	return f, d
}
