package core

import (
	"context"
	"os/exec"
)

func ffmpegCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}
