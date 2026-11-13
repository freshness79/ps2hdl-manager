package chd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

// hunkStream decompresses hunks [from, to) in parallel and delivers them in order.
func (c *File) hunkStream(from, to uint32, fn func(n uint32, data []byte) error) error {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	const batch = 64 // hunks per job
	type job struct {
		start, end uint32
		buf        []byte
		err        error
		done       chan struct{}
	}
	jobs := make(chan *job, workers*2)
	order := make(chan *job, workers*2)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := &decState{}
			defer st.close()
			for j := range jobs {
				for n := j.start; n < j.end && j.err == nil; n++ {
					off := int(n-j.start) * int(c.HunkBytes)
					j.err = c.readHunk(st, n, j.buf[off:off+int(c.HunkBytes)])
				}
				close(j.done)
			}
		}()
	}
	go func() {
		defer close(jobs)
		defer close(order)
		for s := from; s < to; s += batch {
			e := s + batch
			if e > to {
				e = to
			}
			j := &job{start: s, end: e, buf: make([]byte, int(e-s)*int(c.HunkBytes)), done: make(chan struct{})}
			select {
			case order <- j:
			case <-stop:
				return
			}
			select {
			case jobs <- j:
			case <-stop:
				return
			}
		}
	}()
	var err error
	for j := range order {
		<-j.done
		if err == nil && j.err != nil {
			err = j.err
		}
		for n := j.start; n < j.end && err == nil; n++ {
			off := int(n-j.start) * int(c.HunkBytes)
			err = fn(n, j.buf[off:off+int(c.HunkBytes)])
		}
		if err != nil {
			select {
			case <-stop:
			default:
				close(stop)
			}
		}
	}
	wg.Wait()
	return err
}

// ErrNoDataTrack is returned for CHDs without a usable data track.
var ErrNoDataTrack = errors.New("chd: no data track (audio-only or unknown CD layout)")

// WriteISO writes the disc as a plain ISO with 2048-byte sectors.
// DVD CHDs are copied as-is; for CD CHDs the user data of the first data track
// is extracted from each sector (MODE1/2048, MODE1_RAW, MODE2_RAW, MODE2 …).
// progress (optional) receives 0..1.
func (c *File) WriteISO(w io.Writer, progress func(float64)) error {
	switch c.Kind() {
	case "DVD":
		return c.writeRaw(w, progress)
	case "CD":
		return c.writeCDTrack(w, progress)
	}
	if c.UnitBytes == 2048 {
		return c.writeRaw(w, progress)
	}
	return fmt.Errorf("chd: not a CD/DVD image (hard disk or unknown type)")
}

func (c *File) writeRaw(w io.Writer, progress func(float64)) error {
	remaining := c.LogicalBytes
	return c.hunkStream(0, c.HunkCount, func(n uint32, data []byte) error {
		k := uint64(len(data))
		if k > remaining {
			k = remaining
		}
		remaining -= k
		if progress != nil && n%512 == 0 {
			progress(float64(n) / float64(c.HunkCount))
		}
		_, err := w.Write(data[:k])
		return err
	})
}

func (c *File) writeCDTrack(w io.Writer, progress func(float64)) error {
	tracks, err := c.Tracks()
	if err != nil {
		return err
	}
	var tr *Track
	for i := range tracks {
		if tracks[i].DataOff >= 0 {
			if i > 0 {
				return fmt.Errorf("chd: first track is audio: not a PS2 game disc")
			}
			tr = &tracks[i]
			break
		}
	}
	if tr == nil {
		return ErrNoDataTrack
	}
	if c.HunkBytes%cdFrameSize != 0 {
		return fmt.Errorf("chd: unexpected CD hunk size %d", c.HunkBytes)
	}
	framesPerHunk := uint32(c.HunkBytes / cdFrameSize)
	first := uint32(tr.StartFrm)
	last := first + uint32(tr.Frames) // exclusive
	return c.hunkStream(first/framesPerHunk, (last+framesPerHunk-1)/framesPerHunk, func(n uint32, data []byte) error {
		for i := uint32(0); i < framesPerHunk; i++ {
			abs := n*framesPerHunk + i
			if abs < first || abs >= last {
				continue
			}
			fr := data[i*cdFrameSize:]
			if _, err := w.Write(fr[tr.DataOff : tr.DataOff+2048]); err != nil {
				return err
			}
		}
		if progress != nil && n%256 == 0 {
			progress(float64(n*framesPerHunk-first) / float64(tr.Frames))
		}
		return nil
	})
}

// ExtractISO is a convenience wrapper: opens chdPath and writes isoPath.
func ExtractISO(chdPath, isoPath string, progress func(float64)) error {
	c, err := Open(chdPath)
	if err != nil {
		return err
	}
	defer c.Close()
	out, err := os.Create(isoPath)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(out, 4<<20)
	err = c.WriteISO(bw, progress)
	if err == nil {
		err = bw.Flush()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(isoPath)
	}
	return err
}
