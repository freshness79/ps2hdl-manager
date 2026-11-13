package gamedb

// Random access to a large remote ZIP (zip64 included) over HTTP range requests:
// the end of the file and the central directory are read once and cached on
// disk; afterwards each member costs one request (local header + data). Used for
// OPL Manager's art backup on archive.org (5.5 GB, ~100 000 members), of which
// only a few pictures per game are needed.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	rzTail      = 128 << 10 // end of file read to find the (zip64) end of central directory
	rzMaxCD     = 64 << 20  // a larger central directory is not the expected archive
	rzReadAhead = 384 << 10 // one member (local header + data) per request in practice
)

// RemoteZip reads members of a ZIP published on an HTTP server that honours Range.
type RemoteZip struct {
	URL    string
	Size   int64
	client *http.Client
	agent  string

	mu       sync.Mutex // read-ahead chunk
	urlMu    sync.Mutex
	final    string // URL after redirects, reused for later requests
	chunkOff int64
	chunk    []byte

	cdOff, tailOff int64
	cd, tail       []byte
	zr             *zip.Reader
}

type rzHeader struct {
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	CDOff   int64  `json:"cd_off"`
	CDLen   int    `json:"cd_len"`
	TailOff int64  `json:"tail_off"`
	TailLen int    `json:"tail_len"`
}

// OpenRemoteZip opens url, using (and filling) the directory cache in cacheFile.
func OpenRemoteZip(url, cacheFile, agent string) (*RemoteZip, error) {
	z := &RemoteZip{URL: url, agent: agent, client: &http.Client{Timeout: 5 * time.Minute}}
	if err := z.loadCache(cacheFile); err == nil {
		return z, nil
	}
	if err := z.readDirectory(); err != nil {
		return nil, err
	}
	_ = z.saveCache(cacheFile)
	return z, nil
}

// Members lists the member names.
func (z *RemoteZip) Members() []*zip.File { return z.zr.File }

// ReadMember returns a member's content, verified against its CRC-32.
func (z *RemoteZip) ReadMember(f *zip.File, max int64) ([]byte, error) {
	if int64(f.UncompressedSize64) > max {
		return nil, fmt.Errorf("%s: %d bytes, larger than expected", f.Name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err) // includes zip.ErrChecksum
	}
	return b, nil
}

func (z *RemoteZip) readDirectory() error {
	tail, total, err := z.get(-rzTail, rzTail)
	if err != nil {
		return fmt.Errorf("reading the end of the archive: %w", err)
	}
	z.Size, z.tail, z.tailOff = total, tail, total-int64(len(tail))
	cdOff, cdLen, err := findCentralDirectory(tail, z.tailOff)
	if err != nil {
		return err
	}
	if cdLen > rzMaxCD {
		return fmt.Errorf("central directory of %d MB: not the expected archive", cdLen>>20)
	}
	if cdOff+cdLen > z.tailOff { // directory (partly) inside the tail already read
		cdLen = z.tailOff - cdOff
	}
	if cdLen > 0 {
		cd, _, err := z.get(cdOff, cdLen)
		if err != nil {
			return fmt.Errorf("reading the archive index: %w", err)
		}
		z.cd = cd
	}
	z.cdOff = cdOff
	return z.openZip()
}

func (z *RemoteZip) openZip() error {
	zr, err := zip.NewReader(readerAtFunc(z.readAt), z.Size)
	if err != nil {
		return fmt.Errorf("archive index: %w", err)
	}
	z.zr = zr
	return nil
}

// findCentralDirectory locates the central directory from the end of the file.
func findCentralDirectory(tail []byte, tailOff int64) (off, size int64, err error) {
	i := bytes.LastIndex(tail, []byte("PK\x05\x06"))
	if i < 0 || len(tail)-i < 22 {
		return 0, 0, errors.New("end of central directory not found: not a ZIP archive")
	}
	eocd := tail[i:]
	size = int64(binary.LittleEndian.Uint32(eocd[12:16]))
	off = int64(binary.LittleEndian.Uint32(eocd[16:20]))
	if size != 0xffffffff && off != 0xffffffff {
		return off, size, nil
	}
	// zip64: locator right before the EOCD, pointing to the zip64 EOCD record
	if i < 20 || string(tail[i-20:i-16]) != "PK\x06\x07" {
		return 0, 0, errors.New("zip64 locator not found")
	}
	recOff := int64(binary.LittleEndian.Uint64(tail[i-20+8:]))
	r := recOff - tailOff
	if r < 0 || r+56 > int64(len(tail)) || string(tail[r:r+4]) != "PK\x06\x06" {
		return 0, 0, errors.New("zip64 end of central directory not in the last bytes of the file")
	}
	size = int64(binary.LittleEndian.Uint64(tail[r+40:]))
	off = int64(binary.LittleEndian.Uint64(tail[r+48:]))
	return off, size, nil
}

type readerAtFunc func(p []byte, off int64) (int, error)

func (f readerAtFunc) ReadAt(p []byte, off int64) (int, error) { return f(p, off) }

// readAt serves the cached directory and tail from memory and anything else
// with range requests (with read-ahead, so a member is usually one request).
func (z *RemoteZip) readAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= z.Size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off < z.Size {
		var k int
		switch {
		case off >= z.tailOff && off-z.tailOff < int64(len(z.tail)):
			k = copy(p[n:], z.tail[off-z.tailOff:])
		case off >= z.cdOff && off-z.cdOff < int64(len(z.cd)):
			k = copy(p[n:], z.cd[off-z.cdOff:])
		default:
			z.mu.Lock()
			if off < z.chunkOff || off >= z.chunkOff+int64(len(z.chunk)) {
				want := max(int64(len(p)-n), rzReadAhead)
				if off+want > z.Size {
					want = z.Size - off
				}
				b, _, err := z.get(off, want)
				if err != nil {
					z.mu.Unlock()
					return n, err
				}
				z.chunkOff, z.chunk = off, b
			}
			k = copy(p[n:], z.chunk[off-z.chunkOff:])
			z.mu.Unlock()
		}
		n += k
		off += int64(k)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// get fetches length bytes at off (off < 0: the last length bytes) and returns the
// data and the total size of the file. Retries on 429/5xx and network errors.
func (z *RemoteZip) get(off, length int64) ([]byte, int64, error) {
	rng := fmt.Sprintf("bytes=%d-%d", off, off+length-1)
	if off < 0 {
		rng = fmt.Sprintf("bytes=%d", off)
	}
	var lastErr error
	for attempt, wait := 0, time.Second; attempt < 4; attempt, wait = attempt+1, wait*3 {
		if attempt > 0 {
			time.Sleep(wait)
		}
		u := z.URL
		z.urlMu.Lock()
		if z.final != "" {
			u = z.final
		}
		z.urlMu.Unlock()
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Range", rng)
		req.Header.Set("User-Agent", z.agent)
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := z.client.Do(req)
		if err != nil {
			lastErr = err // network problem: retry
			continue
		}
		b, total, err := readRange(resp, length)
		if err == nil {
			z.urlMu.Lock()
			z.final = resp.Request.URL.String()
			z.urlMu.Unlock()
			return b, total, nil
		}
		lastErr = err
		switch {
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			continue // busy server (archive.org often answers 500/503): retry
		case resp.StatusCode == 404 || resp.StatusCode == 403:
			z.urlMu.Lock()
			retry := z.final != ""
			z.final = "" // the data server may have changed: start again from the item URL
			z.urlMu.Unlock()
			if retry {
				continue
			}
		}
		return nil, 0, lastErr
	}
	return nil, 0, lastErr
}

func readRange(resp *http.Response, length int64) ([]byte, int64, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		// 200 would be the whole multi-GB file: never read it
		return nil, 0, fmt.Errorf("HTTP %d from %s (range request not honoured)", resp.StatusCode, resp.Request.URL.Host)
	}
	cr := resp.Header.Get("Content-Range") // bytes a-b/total
	slash := strings.LastIndexByte(cr, '/')
	total, err := strconv.ParseInt(cr[slash+1:], 10, 64)
	if slash < 0 || err != nil {
		return nil, 0, fmt.Errorf("bad Content-Range %q", cr)
	}
	limit := length
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > limit {
		return nil, 0, errors.New("server sent more than requested")
	}
	return b, total, nil
}

func (z *RemoteZip) saveCache(path string) error {
	if path == "" {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	h, _ := json.Marshal(rzHeader{URL: z.URL, Size: z.Size, CDOff: z.cdOff, CDLen: len(z.cd), TailOff: z.tailOff, TailLen: len(z.tail)})
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = f.Write(append(append(h, '\n'), append(append([]byte{}, z.cd...), z.tail...)...))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (z *RemoteZip) loadCache(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		return errors.New("bad cache")
	}
	var h rzHeader
	if err := json.Unmarshal(b[:nl], &h); err != nil || h.URL != z.URL {
		return errors.New("cache for another archive")
	}
	body := b[nl+1:]
	if len(body) != h.CDLen+h.TailLen {
		return errors.New("truncated cache")
	}
	z.Size, z.cdOff, z.tailOff = h.Size, h.CDOff, h.TailOff
	z.cd, z.tail = body[:h.CDLen], body[h.CDLen:]
	return z.openZip()
}
