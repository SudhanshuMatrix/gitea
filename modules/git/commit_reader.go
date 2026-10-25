// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bytes"
	"fmt"
	"io"

	"gitea.dev/modules/util"
)

const (
	commitHeaderGpgsig       = "gpgsig"
	commitHeaderGpgsigSha256 = "gpgsig-sha256"
)

func assignCommitFields(commit *Commit, headerKey string, headerValue []byte) error {
	if len(headerValue) > 0 && headerValue[len(headerValue)-1] == '\n' {
		headerValue = headerValue[:len(headerValue)-1] // remove trailing newline
	}
	switch headerKey {
	case "tree":
		objID, err := NewIDFromString(string(headerValue))
		if err != nil {
			return fmt.Errorf("invalid tree ID %q: %w", string(headerValue), err)
		}
		commit.TreeID = objID
	case "parent":
		objID, err := NewIDFromString(string(headerValue))
		if err != nil {
			return fmt.Errorf("invalid parent ID %q: %w", string(headerValue), err)
		}
		commit.Parents = append(commit.Parents, objID)
	case "author":
		commit.Author.Decode(headerValue)
	case "committer":
		commit.Committer.Decode(headerValue)
	case commitHeaderGpgsig, commitHeaderGpgsigSha256:
		// if there are duplicate "gpgsig" and "gpgsig-sha256" headers, then the signature must have already been invalid
		// so we don't need to handle duplicate headers here
		commit.Signature = &CommitSignature{Signature: string(headerValue)}
	}
	return nil
}

// CommitFromReader will generate a Commit from a provided reader
// We need this to interpret commits from cat-file or cat-file --batch
//
// If used as part of a cat-file --batch stream you need to limit the reader to the correct size
func CommitFromReader(objectID ObjectID, reader io.Reader) (*Commit, error) {
	commit := &Commit{
		ID:        objectID,
		Author:    &Signature{},
		Committer: &Signature{},
	}

	buf := &bytes.Buffer{}
	if limited, ok := reader.(*io.LimitedReader); ok {
		buf.Grow(int(min(limited.N, 4*1024)) + bytes.MinRead) // the limit is usually the object size, capped in case it is only an upper bound
	}
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("unable to read commit %q: %w", objectID.String(), err)
	}
	data := buf.Bytes() // never modified below, so the parsed fields can reference it

	inHeader := true
	var payload []byte // only built when a signature header is found
	payloadCopied, messageStart := 0, len(data)
	var headerKey string
	var headerValue []byte
	for pos := 0; pos < len(data); {
		lineEnd := len(data)
		if idx := bytes.IndexByte(data[pos:], '\n'); idx != -1 {
			lineEnd = pos + idx + 1
		}
		line := data[pos:lineEnd:lineEnd]
		if !inHeader {
			messageStart = pos
			break
		}

		inHeader = !(len(line) == 1 && line[0] == '\n') // still in header if line is not just a newline
		k, v, _ := bytes.Cut(line, []byte{' '})
		if len(k) != 0 || !inHeader {
			if headerKey != "" {
				if err := assignCommitFields(commit, headerKey, headerValue); err != nil {
					return nil, fmt.Errorf("unable to parse commit %q: %w", objectID.String(), err)
				}
			}
			headerKey = util.UnsafeBytesToString(k) // it also resets the headerValue to empty string if not inHeader
			headerValue = v
		} else {
			headerValue = append(headerValue, v...) // "line" is capped, so it never writes into "data"
		}
		if headerKey == commitHeaderGpgsig || headerKey == commitHeaderGpgsigSha256 {
			payload = append(payload, data[payloadCopied:pos]...)
			payloadCopied = lineEnd
		}
		pos = lineEnd
	}

	commit.MessageRaw = util.UnsafeBytesToString(data[messageStart:])
	if commit.Signature != nil {
		commit.Signature.Payload = string(append(payload, data[payloadCopied:]...))
	}
	return commit, nil
}
