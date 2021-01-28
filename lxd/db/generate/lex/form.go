package lex

// Plural converts to plural form ("foo" -> "foos")
func Plural(s string) string {
	// TODO: smarter algorithm? :)
	return s + "s"
}

// Singular converts to singular form ("foos" -> "foo")
func Singular(s string) string {
	// TODO: smarter algorithm? :)
	return s[:len(s)-1]
}
