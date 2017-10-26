package common

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	getter "github.com/hashicorp/go-getter"
)

// DownloadConfig is the configuration given to instantiate a new
// download instance. Once a configuration is used to instantiate
// a download client, it must not be modified.
type DownloadConfig struct {
	// The source URL in the form of a string.
	Url string

	// This is the path to download the file to.
	TargetPath string

	// DownloaderMap maps a schema to a Download.
	DownloaderMap map[string]Downloader

	// If true, this will copy even a local file to the target
	// location. If false, then it will "download" the file by just
	// returning the local path to the file.
	CopyFile bool

	// The hashing implementation to use to checksum the downloaded file.
	Hash hash.Hash

	// The checksum for the downloaded file. The hash implementation configuration
	// for the downloader will be used to verify with this checksum after
	// it is downloaded.
	Checksum []byte

	// What to use for the user agent for HTTP requests. If set to "", use the
	// default user agent provided by Go.
	UserAgent string
}

// A DownloadClient helps download, verify checksums, etc.
type DownloadClient struct {
	config     *DownloadConfig
	downloader Downloader
}

// NewDownloadClient returns a new DownloadClient for the given
// configuration.
func NewDownloadClient(c *DownloadConfig) *DownloadClient {
	if c.DownloaderMap == nil {
		c.DownloaderMap = map[string]Downloader{
			"http":  &HTTPDownloader{userAgent: c.UserAgent},
			"https": &HTTPDownloader{userAgent: c.UserAgent},
		}
	}

	return &DownloadClient{config: c}
}

// A downloader is responsible for actually taking a remote URL and
// downloading it.
type Downloader interface {
	Cancel()
	Download(*os.File, *url.URL) error
	Progress() uint
	Total() uint
}

func (d *DownloadClient) Cancel() {
	// TODO(mitchellh): Implement
}

func (d *DownloadClient) Get() (string, error) {
	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	gc := getter.Client{
		Src:  d.config.Url,
		Dst:  d.config.TargetPath,
		Pwd:  pwd,
		Mode: getter.ClientModeFile,
		Dir:  false}

	err = gc.Get()
	if err != nil {
		log.Printf("Error Getting URL: %s", err)
		return "", err
	}

	return d.config.TargetPath, err
}

// PercentProgress returns the download progress as a percentage.
func (d *DownloadClient) PercentProgress() int {
	if d.downloader == nil {
		return -1
	}

	return int((float64(d.downloader.Progress()) / float64(d.downloader.Total())) * 100)
}

// HTTPDownloader is an implementation of Downloader that downloads
// files over HTTP.
type HTTPDownloader struct {
	progress  uint
	total     uint
	userAgent string
}

func (*HTTPDownloader) Cancel() {
	// TODO(mitchellh): Implement
}

func (d *HTTPDownloader) Download(dst *os.File, src *url.URL) error {
	log.Printf("Starting download: %s", src.String())

	// Seek to the beginning by default
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}

	// Reset our progress
	d.progress = 0

	// Make the request. We first make a HEAD request so we can check
	// if the server supports range queries. If the server/URL doesn't
	// support HEAD requests, we just fall back to GET.
	req, err := http.NewRequest("HEAD", src.String(), nil)
	if err != nil {
		return err
	}

	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	resp, err := httpClient.Do(req)
	if err == nil && (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		// If the HEAD request succeeded, then attempt to set the range
		// query if we can.
		if resp.Header.Get("Accept-Ranges") == "bytes" {
			if fi, err := dst.Stat(); err == nil {
				if _, err = dst.Seek(0, os.SEEK_END); err == nil {
					req.Header.Set("Range", fmt.Sprintf("bytes=%d-", fi.Size()))
					d.progress = uint(fi.Size())
				}
			}
		}
	}

	// Set the request to GET now, and redo the query to download
	req.Method = "GET"

	resp, err = httpClient.Do(req)
	if err != nil {
		return err
	}

	d.total = d.progress + uint(resp.ContentLength)
	var buffer [4096]byte
	for {
		n, err := resp.Body.Read(buffer[:])
		if err != nil && err != io.EOF {
			return err
		}

		d.progress += uint(n)

		if _, werr := dst.Write(buffer[:n]); werr != nil {
			return werr
		}

		if err == io.EOF {
			break
		}
	}

	return nil
}

func (d *HTTPDownloader) Progress() uint {
	return d.progress
}

func (d *HTTPDownloader) Total() uint {
	return d.total
}
