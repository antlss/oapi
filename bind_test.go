package oapi

import (
	"mime/multipart"
	"reflect"
	"testing"
)

// fileHeader is a tiny multipart.FileHeader factory for the assignFiles tests.
func fileHeader(name string) *multipart.FileHeader {
	return &multipart.FileHeader{Filename: name} //nolint:exhaustruct
}

// TestAssignFiles_FlatFields covers the common case: a single-file field and a
// file-slice field on a flat struct, plus a non-file field that must be ignored.
func TestAssignFiles_FlatFields(t *testing.T) {
	type body struct {
		Avatar *multipart.FileHeader   `form:"avatar"`
		Docs   []*multipart.FileHeader `form:"docs"`
		Note   string                  `form:"note"`
	}
	files := map[string][]*multipart.FileHeader{
		"avatar": {fileHeader("a.png")},
		"docs":   {fileHeader("d1.pdf"), fileHeader("d2.pdf")},
	}

	var b body
	assignFiles(reflect.ValueOf(&b).Elem(), files)

	if b.Avatar == nil || b.Avatar.Filename != "a.png" {
		t.Fatalf("Avatar = %+v, want a.png", b.Avatar)
	}
	if len(b.Docs) != 2 {
		t.Fatalf("Docs len = %d, want 2", len(b.Docs))
	}
}

// TestAssignFiles_EmbeddedValueStruct covers recursion into an embedded (value)
// struct that carries the file field.
func TestAssignFiles_EmbeddedValueStruct(t *testing.T) {
	type Attachments struct {
		File *multipart.FileHeader `form:"file"`
	}
	type body struct {
		Attachments
		Title string `form:"title"`
	}

	var b body
	assignFiles(reflect.ValueOf(&b).Elem(),
		map[string][]*multipart.FileHeader{"file": {fileHeader("x.bin")}})

	if b.File == nil || b.File.Filename != "x.bin" {
		t.Fatalf("embedded File = %+v, want x.bin", b.File)
	}
}

// TestAssignFiles_EmbeddedPointerAllocatedOnlyWhenTargeted pins the subtle rule
// the deepest branch encodes: a nil embedded *Struct is materialised only when an
// upload actually targets a file field inside it, never otherwise.
func TestAssignFiles_EmbeddedPointerAllocatedOnlyWhenTargeted(t *testing.T) {
	type Attachments struct {
		File *multipart.FileHeader `form:"file"`
	}
	type body struct {
		*Attachments
		Title string `form:"title"`
	}

	// A matching upload allocates the embedded pointer and sets the file.
	var withFile body
	assignFiles(reflect.ValueOf(&withFile).Elem(),
		map[string][]*multipart.FileHeader{"file": {fileHeader("y.bin")}})
	if withFile.Attachments == nil {
		t.Fatal("embedded *Attachments was not allocated despite a matching upload")
	}
	if withFile.File == nil || withFile.File.Filename != "y.bin" {
		t.Fatalf("embedded pointer File = %+v, want y.bin", withFile.File)
	}

	// No matching upload leaves the nil embedded pointer untouched.
	var noFile body
	assignFiles(reflect.ValueOf(&noFile).Elem(),
		map[string][]*multipart.FileHeader{"other": {fileHeader("z.bin")}})
	if noFile.Attachments != nil {
		t.Fatal("embedded *Attachments was materialised with no matching upload")
	}
}
