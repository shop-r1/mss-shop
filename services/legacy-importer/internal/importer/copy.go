package importer

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"

	"github.com/jackc/pgx/v5/pgconn"
)

type copyToEndpoint interface {
	CopyTo(context.Context, io.Writer, string) (int64, error)
}

type copyFromEndpoint interface {
	CopyFrom(context.Context, io.Reader, string) (int64, error)
}

type pgCopyTo struct{ connection *pgconn.PgConn }

func (endpoint pgCopyTo) CopyTo(ctx context.Context, writer io.Writer, sql string) (int64, error) {
	tag, err := endpoint.connection.CopyTo(ctx, writer, sql)
	return tag.RowsAffected(), err
}

type pgCopyFrom struct{ connection *pgconn.PgConn }

func (endpoint pgCopyFrom) CopyFrom(ctx context.Context, reader io.Reader, sql string) (int64, error) {
	tag, err := endpoint.connection.CopyFrom(ctx, reader, sql)
	return tag.RowsAffected(), err
}

type copyResult struct {
	rows int64
	err  error
}

type copyEvidence struct {
	Rows   int64
	SHA256 [sha256.Size]byte
}

// streamBinaryCopy keeps row values inside a bounded database-to-database
// pipe. Only command row counts leave this function.
func streamBinaryCopy(
	ctx context.Context,
	source copyToEndpoint,
	target copyFromEndpoint,
	sourceSQL string,
	targetSQL string,
) (copyEvidence, error) {
	copyContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	hasher := sha256.New()
	resultChannel := make(chan copyResult, 1)
	go func() {
		rows, err := source.CopyTo(copyContext, io.MultiWriter(writer, hasher), sourceSQL)
		if err != nil {
			_ = writer.CloseWithError(errors.New("source COPY stream failed"))
		} else {
			_ = writer.Close()
		}
		resultChannel <- copyResult{rows: rows, err: err}
	}()

	targetRows, targetErr := target.CopyFrom(copyContext, reader, targetSQL)
	if targetErr != nil {
		cancel()
		_ = reader.CloseWithError(errors.New("target COPY stream failed"))
	} else {
		_ = reader.Close()
	}
	sourceResult := <-resultChannel
	if sourceResult.err != nil {
		return copyEvidence{}, errors.New("source COPY stream failed")
	}
	if targetErr != nil {
		return copyEvidence{}, errors.New("target COPY stream failed")
	}
	if sourceResult.rows < 0 || targetRows < 0 || sourceResult.rows != targetRows {
		return copyEvidence{}, errors.New("binary COPY row counts did not match")
	}
	evidence := copyEvidence{Rows: targetRows}
	copy(evidence.SHA256[:], hasher.Sum(nil))
	return evidence, nil
}

func hashBinaryCopy(
	ctx context.Context,
	source copyToEndpoint,
	sourceSQL string,
) (copyEvidence, error) {
	hasher := sha256.New()
	rows, err := source.CopyTo(ctx, hasher, sourceSQL)
	if err != nil || rows < 0 {
		return copyEvidence{}, errors.New("verify target COPY stream failed")
	}
	evidence := copyEvidence{Rows: rows}
	copy(evidence.SHA256[:], hasher.Sum(nil))
	return evidence, nil
}
