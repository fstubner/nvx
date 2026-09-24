//go:build !windows

package nvx

import (
	"fmt"
	"os"
)

func createDirLink(link, target string) error {
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("failed to create symbolic link: %w", err)
	}
	return nil
}
