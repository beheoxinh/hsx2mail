//go:build !linux

package platform

func screenSize() (int, int) {
	return 0, 0
}
