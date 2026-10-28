package gopeed

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Download interface {
	Resolve(request *http.Request) (*http.Response, error)
	Down(request *http.Request, filename string) error
}

// Resolve return the file response to be downloaded
func Resolve(request *Request) (*Response, error) {
	httpRequest, err := BuildHTTPRequest(request)
	if err != nil {
		return nil, err
	}
	// Use "Range" header to resolve
	httpRequest.Header.Set("Range", "bytes=0-0")
	httpClient := BuildHTTPClient()
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 206 {
		return nil, fmt.Errorf("response status error:%d", response.StatusCode)
	}
	ret := &Response{}
	// Get file name by "Content-Disposition"
	contentDisposition := response.Header.Get("Content-Disposition")
	if contentDisposition != "" {
		_, params, _ := mime.ParseMediaType(contentDisposition)
		filename := params["filename"]
		if filename != "" {
			ret.Name = filename
		}
	}
	// Get file name by URL
	if ret.Name == "" {
		parse, err := url.Parse(httpRequest.URL.String())
		if err == nil {
			// e.g. /files/test.txt => test.txt
			ret.Name = subLastSlash(parse.Path)
		}
	}
	// Unknow file name
	if ret.Name == "" {
		ret.Name = "unknow"
	}
	ret.Name = filepath.Base(strings.ReplaceAll(ret.Name, "\\", "/"))
	ret.Range = response.StatusCode == http.StatusPartialContent
	if ret.Range {
		var start, end int64
		if _, err := fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &ret.Size); err != nil || start != 0 || end != 0 || ret.Size <= 0 {
			return nil, fmt.Errorf("invalid Content-Range: %q", response.Header.Get("Content-Range"))
		}
	} else {
		ret.Size = response.ContentLength
	}
	return ret, nil
}

// Down downloads into an adjacent temporary file and only publishes it after
// every chunk has been received and validated.
func Down(request *Request, to string) error {
	response, err := Resolve(request)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(to), ".v2raya-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if response.Range {
		workers := int64(4)
		if response.Size < workers {
			workers = response.Size
		}
		results := make(chan error, workers)
		for i := int64(0); i < workers; i++ {
			start, end := response.Size*i/workers, response.Size*(i+1)/workers-1
			go func() { results <- downChunk(ctx, request, response, file, start, end) }()
		}
		for i := int64(0); i < workers; i++ {
			if chunkErr := <-results; chunkErr != nil && err == nil {
				err = chunkErr
				cancel()
			}
		}
	} else {
		err = downChunk(ctx, request, response, file, 0, response.Size-1)
	}
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), to)
}

func subLastSlash(str string) string {
	index := strings.LastIndex(str, "/")
	if index != -1 {
		return str[index+1:]
	}
	return ""
}

func BuildHTTPRequest(request *Request) (*http.Request, error) {
	// Build request
	httpRequest, err := http.NewRequest(strings.ToUpper(request.Method), request.URL, bytes.NewReader(request.Content))
	if err != nil {
		return nil, err
	}
	for k, v := range request.Header {
		httpRequest.Header.Add(k, v)
	}
	return httpRequest, nil
}

func BuildHTTPClient() *http.Client {
	// Cookie handle
	jar, _ := cookiejar.New(nil)

	return &http.Client{Jar: jar, Timeout: 2 * time.Minute}
}

func downChunk(ctx context.Context, request *Request, response *Response, file *os.File, start, end int64) error {
	httpRequest, err := BuildHTTPRequest(request)
	if err != nil {
		return err
	}
	httpRequest = httpRequest.WithContext(ctx)
	if response.Range {
		httpRequest.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	} else {
		httpRequest.Header.Del("Range")
	}
	httpResponse, err := BuildHTTPClient().Do(httpRequest)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	if response.Range {
		expected := fmt.Sprintf("bytes %d-%d/%d", start, end, response.Size)
		if httpResponse.StatusCode != http.StatusPartialContent || httpResponse.Header.Get("Content-Range") != expected {
			return fmt.Errorf("invalid range response: status=%d range=%q", httpResponse.StatusCode, httpResponse.Header.Get("Content-Range"))
		}
	} else if httpResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("download status error: %d", httpResponse.StatusCode)
	}
	var reader io.Reader = httpResponse.Body
	if response.Range {
		reader = io.LimitReader(reader, end-start+2)
	}
	written, err := io.Copy(io.NewOffsetWriter(file, start), reader)
	if err != nil {
		return err
	}
	if response.Range && written != end-start+1 || !response.Range && response.Size >= 0 && written != response.Size {
		return fmt.Errorf("download size mismatch: received %d bytes", written)
	}
	return nil
}
