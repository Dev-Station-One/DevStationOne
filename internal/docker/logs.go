package docker

import (
	"bufio"
	"context"
	"io"
	"strconv"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// FollowLogs streams a scoped container's logs to fn until ctx is cancelled
// or the stream ends. It replays Tail past lines first (when tail > 0) and
// follows (docker logs -f semantics) for both running and stopped
// containers — a stopped container's stream simply ends at the end of its
// retained log.
func FollowLogs(ctx context.Context, projectDir, containerID string, tail int, fn func(line string)) error {
	c, _, err := dial(ctx)
	if err != nil {
		return err
	}
	id, err := scopedID(ctx, projectDir, containerID)
	if err != nil {
		return err
	}
	opts := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}
	if tail > 0 {
		opts.Tail = strconv.Itoa(tail)
	}
	rc, err := c.ContainerLogs(ctx, id, opts)
	if err != nil {
		return err
	}
	defer rc.Close()

	// Demultiplex stdout/stderr into lines (the non-TTY default); a TTY
	// container's raw stream has no 8-byte headers and stdcopy passes the
	// bytes through untouched.
	r, w := io.Pipe()
	go func() {
		_, _ = stdcopy.StdCopy(w, w, rc)
		w.Close()
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		fn(sc.Text())
	}
	return sc.Err()
}
