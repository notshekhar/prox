package systemone

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

// maxBody is System One's request limit; base64 or multipart, images included.
const maxBody = 32 << 20

// requestPart names the multipart part that holds the JSON request.
const requestPart = "request"

// body is a request as sent: the JSON, and any multipart files by part name.
type body struct {
	json  []byte
	files map[string][]byte
}

// readBody reads a JSON body, or a multipart one whose "request" part is the
// JSON and whose other parts are files.
func readBody(w http.ResponseWriter, r *http.Request) (body, *statusError) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return body{}, readError(err)
		}
		return body{json: data}, nil
	}
	return readMultipart(multipart.NewReader(r.Body, params["boundary"]))
}

func readMultipart(mr *multipart.Reader) (body, *statusError) {
	b := body{files: map[string][]byte{}}
	for first := true; ; first = false {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return body{}, readError(err)
		}
		name := part.FormName()
		if err := checkPartName(name, first, b); err != nil {
			return body{}, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return body{}, readError(err)
		}
		if name == requestPart {
			b.json = data
		} else {
			b.files[name] = data
		}
	}
	if b.json == nil {
		return body{}, badRequest("a multipart request needs a %q part holding the JSON", requestPart)
	}
	return b, nil
}

func checkPartName(name string, first bool, b body) *statusError {
	switch {
	case first && name != requestPart:
		return badRequest("the first multipart part must be %q", requestPart)
	case !first && name == requestPart:
		return badRequest("only one %q part is allowed", requestPart)
	case strings.TrimSpace(name) == "":
		return badRequest("every multipart part needs a name")
	}
	if _, dup := b.files[name]; dup {
		return badRequest("multipart part %q appears twice", name)
	}
	return nil
}

func readError(err error) *statusError {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return tooLarge("the request is larger than %d MiB", maxBody>>20)
	}
	return badRequest("could not read the request body")
}
