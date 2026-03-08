package tools

import (
	"path/filepath"
)

type MockMapper struct {
	Dir string
}

func (m *MockMapper) ToSandbox(path string) string {
	return filepath.Join(m.Dir, path)
}
func (m *MockMapper) ToVirtual(path string) string {
	return path
}
func (m *MockMapper) SanitizeText(text string) string {
	return text
}
func (m *MockMapper) SecurePath(path string) (string, bool) {
	return filepath.Join(m.Dir, path), true
}
