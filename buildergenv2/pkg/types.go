package pkg

type Struct struct {
	Package string
	Name    string
	Fields  []Field
	Imports map[string]string
	Pointer bool
}

type Field struct {
	Name       string
	Type       string
	Tags       string
	Optional   bool
	FromString bool
}

type Structs struct {
	Package string
	Imports map[string]string
	Structs []*Struct
}
