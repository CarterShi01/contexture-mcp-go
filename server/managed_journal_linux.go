//go:build linux

package server

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func journalOpen(p string, flags int, mode os.FileMode) (*os.File, error) {
	fd, e := unix.Open(p, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode))
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), p), nil
}
func journalOpenDir(p string) (*os.File, error) {
	return journalOpen(p, unix.O_RDONLY|unix.O_DIRECTORY, 0)
}
func journalLock(f *os.File) error {
	e := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(e, unix.EAGAIN) || errors.Is(e, unix.EACCES) || errors.Is(e, unix.EDEADLK) {
		return &JournalError{"store_in_use", "Store has an exclusive lifetime owner"}
	}
	return e
}
func journalNoReplace(a, b string) error {
	// renameat2 never creates an intermediate hard link and never overwrites.
	// ENOSYS/EINVAL are refused, not replaced with a weaker rename fallback.
	e := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_NOREPLACE)
	if errors.Is(e, unix.ENOSYS) || errors.Is(e, unix.EINVAL) || errors.Is(e, unix.EOPNOTSUPP) {
		return &JournalError{"durability_unsupported", "Atomic no-replace publication is unavailable"}
	}
	return e
}
func journalPrivate(s os.FileInfo) error {
	t, ok := s.Sys().(*syscall.Stat_t)
	if !ok || t.Uid != uint32(os.Geteuid()) || s.Mode().Perm()&0077 != 0 {
		return &JournalError{"unsafe_permissions", "Store assets must be private to owner"}
	}
	if !s.IsDir() && (!s.Mode().IsRegular() || t.Nlink != 1) {
		return &JournalError{"unsafe_path", "Expected single-link regular file"}
	}
	return nil
}
func journalPlatform(root, profile string) error {
	if profile != "strict" && profile != "process_crash" {
		return &JournalError{"durability_unsupported", "Unknown durability profile"}
	}
	for _, p := range strings.Split(root, "/") {
		p = strings.ToLower(p)
		if strings.HasPrefix(p, "onedrive") || strings.HasPrefix(p, "dropbox") || strings.HasPrefix(p, "google drive") {
			return &JournalError{"durability_unsupported", "Cloud-synchronized storage unsupported"}
		}
	}
	for p := root; ; p = filepath.Dir(p) {
		if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
			return &JournalError{"unsafe_path", "Store must stay outside source worktrees"}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	b, e := os.ReadFile("/proc/self/mountinfo")
	if e != nil {
		return e
	}
	best := -1
	kind := "unknown"
	for _, line := range strings.Split(string(b), "\n") {
		l, r, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(l)
		right := strings.Fields(r)
		if len(fields) < 5 || len(right) == 0 {
			continue
		}
		mount := fields[4]
		for _, esc := range []string{"040", "011", "012", "134"} {
			n, _ := strconv.ParseUint(esc, 8, 8)
			mount = strings.ReplaceAll(mount, "\\"+esc, string(rune(n)))
		}
		if (root == mount || strings.HasPrefix(root, strings.TrimSuffix(mount, "/")+"/")) && len(mount) > best {
			best = len(mount)
			kind = right[0]
		}
	}
	if kind != "ext4" && kind != "xfs" && kind != "btrfs" && !(profile == "process_crash" && (kind == "tmpfs" || kind == "overlay")) {
		return &JournalError{"durability_unsupported", "Unsupported local filesystem: " + kind}
	}
	return nil
}
