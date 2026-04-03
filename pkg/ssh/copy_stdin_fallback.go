package ssh

import (
	"io"
	"os"
	"sync"
)

// fallbackCopyStdinTo 是不支持 poll 时的兜底实现，使用 io.Copy。
// cancel 调用后立即通过 done 通知调用方，避免阻塞等待。
// 后台 goroutine 仍会阻塞在 stdin.Read 直到下次输入，但不会影响调用方继续执行。
func fallbackCopyStdinTo(dst io.Writer) (cancel func(), done <-chan struct{}) {
	ch := make(chan struct{})
	once := &sync.Once{}
	closeCh := func() { once.Do(func() { close(ch) }) }

	go func() {
		defer closeCh()
		_, _ = io.Copy(dst, os.Stdin)
	}()
	return closeCh, ch
}
