//go:build !windows

package server

import "syscall"

// freeDiskBytes renvoie l'espace libre (octets) du volume contenant path.
func freeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
