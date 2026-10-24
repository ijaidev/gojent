package tool

import (
	"encoding/json"
	"reflect"
)

var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

// implementsJSONMarshaler reports whether t or *t has a custom MarshalJSON
// method. Such a type can emit any JSON shape, so schema generation cannot
// infer one from its fields.
func implementsJSONMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)
}
