//go:build !linux

package server

import "os"

func journalUnsupported() error {
	return &JournalError{"durability_unsupported", "Native private locking and durability primitives are unverified on this platform"}
}
func journalPlatform(string, string) error                   { return journalUnsupported() }
func journalOpen(string, int, os.FileMode) (*os.File, error) { return nil, journalUnsupported() }
func journalOpenDir(string) (*os.File, error)                { return nil, journalUnsupported() }
func journalLock(*os.File) error                             { return journalUnsupported() }
func journalNoReplace(string, string) error                  { return journalUnsupported() }
func journalPrivate(os.FileInfo) error                       { return journalUnsupported() }
