// Package resources holds resource descriptors used by the admin/dashboard and
// API registries. Generated resource files (one per resource) populate it.
package resources

// Descriptor describes a generated resource.
type Descriptor struct {
	Name   string
	Table  string
	Fields []FieldDescriptor
}

// FieldDescriptor describes a single resource field.
type FieldDescriptor struct {
	Name     string
	Type     string
	Nullable bool
}
