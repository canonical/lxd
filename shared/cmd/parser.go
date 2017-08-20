package cmd

import (
	"reflect"
	"strings"
	"unsafe"

	"github.com/lxc/lxd/shared/gnuflag"
)

// Parser for command line arguments.
type Parser struct {
	Context      *Context
	UsageMessage string
	ExitOnError  bool
}

// NewParser returns a Parser connected to the given I/O context and printing
// the given usage message when '--help' or '-h' are passed.
func NewParser(context *Context, usage string) *Parser {
	return &Parser{
		Context:      context,
		UsageMessage: usage,
		ExitOnError:  true,
	}
}

// Parse a command line populating the given args object accordingly.
//
// The command line format is expected to be:
//
// <cmd> [subcmd [params]] [flags] [-- [extra]]
//
// The args object may have Subcommand, Params and Extra attributes
// (respectively of type string, []string and []string), which will be
// populated with the subcommand, its params and any extra argument (if
// present).
//
// The type of the args object must have one attribute for each supported
// command line flag, annotated with a tag like `flag:"<name>"`, where <name>
// is the name of the command line flag.
//
// In case of parsing error (e.g. unknown command line flag) the default
// behavior is to call os.Exit() with a non-zero value. This can be disabled by
// setting the ExitOnError attribute to false, in which case the error will be
// returned.
func (p *Parser) Parse(line []string, args interface{}) error {
	val := reflect.ValueOf(args).Elem()

	if err := p.parseFlags(line, val); err != nil {
		return err
	}

	p.parseRest(line, val)

	return nil
}

// Populate the given FlagSet by introspecting the given object, adding a new
// flag variable for each annotated attribute.
func (p *Parser) parseFlags(line []string, val reflect.Value) error {
	mode := gnuflag.ContinueOnError
	if p.ExitOnError {
		mode = gnuflag.ExitOnError
	}

	flags := gnuflag.NewFlagSet(line[0], mode)
	flags.SetOutput(p.Context.stderr)

	if p.UsageMessage != "" {
		// Since usage will be printed only if "-h" or "--help" are
		// explicitly set in the command line, use stdout for it.
		flags.Usage = func() {
			p.Context.Output(p.UsageMessage)
		}
	}

	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Tag.Get("flag")
		if name == "" {
			continue
		}
		kind := typ.Field(i).Type.Kind()
		addr := val.Field(i).Addr()
		switch kind {
		case reflect.Bool:
			pointer := (*bool)(unsafe.Pointer(addr.Pointer()))
			flags.BoolVar(pointer, name, false, "")
		case reflect.String:
			pointer := (*string)(unsafe.Pointer(addr.Pointer()))
			flags.StringVar(pointer, name, "", "")
		case reflect.Int:
			pointer := (*int)(unsafe.Pointer(addr.Pointer()))
			flags.IntVar(pointer, name, -1, "")
		case reflect.Int64:
			pointer := (*int64)(unsafe.Pointer(addr.Pointer()))
			flags.Int64Var(pointer, name, -1, "")
		}
	}

	return flags.Parse(true, line[1:])
}

// Parse any non-flag argument, i.e. the subcommand, its parameters and any
// extra argument following "--".
func (p *Parser) parseRest(line []string, val reflect.Value) {
	subcommand := ""
	params := []string{}
	extra := []string{}
	if len(line) > 1 {
		rest := line[1:]
		for i, token := range rest {
			if token == "--" {
				// Set extra to anything left, excluding the token.
				if i < len(rest)-1 {
					extra = rest[i+1:]
				}
				break
			}
			if strings.HasPrefix(token, "-") {
				// Subcommand and parameters must both come
				// before any flag.
				break
			}
			if i == 0 {
				subcommand = token
				continue
			}
			params = append(params, token)
		}
	}
	if field := val.FieldByName("Subcommand"); field.IsValid() {
		field.SetString(subcommand)
	}
	if field := val.FieldByName("Params"); field.IsValid() {
		field.Set(reflect.ValueOf(params))
	}
	if field := val.FieldByName("Extra"); field.IsValid() {
		field.Set(reflect.ValueOf(extra))
	}
}
