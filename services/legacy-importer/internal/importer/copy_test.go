package importer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type fakeCopySource struct {
	payload []byte
	rows    int64
	err     error
}

func (source fakeCopySource) CopyTo(_ context.Context, writer io.Writer, _ string) (int64, error) {
	if source.err != nil {
		return 0, source.err
	}
	if _, err := writer.Write(source.payload); err != nil {
		return 0, err
	}
	return source.rows, nil
}

type fakeCopyTarget struct {
	payload *bytes.Buffer
	rows    int64
	err     error
}

func (target fakeCopyTarget) CopyFrom(_ context.Context, reader io.Reader, _ string) (int64, error) {
	if target.err != nil {
		return 0, target.err
	}
	if _, err := io.Copy(target.payload, reader); err != nil {
		return 0, err
	}
	return target.rows, nil
}

func TestStreamBinaryCopyTransfersWithoutMaterializingRows(t *testing.T) {
	payload := []byte("postgres-binary-copy-payload")
	received := &bytes.Buffer{}
	evidence, err := streamBinaryCopy(
		context.Background(),
		fakeCopySource{payload: payload, rows: 7},
		fakeCopyTarget{payload: received, rows: 7},
		"source sql",
		"target sql",
	)
	if err != nil || evidence.Rows != 7 || !bytes.Equal(received.Bytes(), payload) {
		t.Fatalf("streamBinaryCopy() rows=%d err=%v payload=%q", evidence.Rows, err, received.Bytes())
	}
	wantHash, err := hashBinaryCopy(context.Background(), fakeCopySource{payload: payload, rows: 7}, "verify sql")
	if err != nil || wantHash != evidence {
		t.Fatalf("hashBinaryCopy() evidence=%#v err=%v, want %#v", wantHash, err, evidence)
	}
}

func TestStreamBinaryCopyRedactsEndpointErrors(t *testing.T) {
	const secret = "business-row-secret"
	_, err := streamBinaryCopy(
		context.Background(),
		fakeCopySource{err: errors.New(secret)},
		fakeCopyTarget{payload: &bytes.Buffer{}},
		"source sql",
		"target sql",
	)
	if err == nil || bytes.Contains([]byte(err.Error()), []byte(secret)) {
		t.Fatalf("streamBinaryCopy() error = %v", err)
	}
}
