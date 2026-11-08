package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"gitee.com/kobeyoung81/nanoartifacts/internal/store"
)

// workViewURL is the link an agent gets back. It points at the portal route,
// not at the service's own root: only that route carries the portal chrome, and
// only it survives a hard navigation (new tab, pasted link).
func workViewURL(id string) string {
	return "/artifacts?work=" + url.QueryEscape(id)
}

func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func versionOf(v *store.Version) versionJSON {
	return versionJSON{
		Seq:         v.Seq,
		ContentHash: v.ContentHash,
		SizeBytes:   v.SizeBytes,
		Note:        v.Note,
		CreatedBy:   v.CreatedBy,
		CreatedAt:   nowRFC3339(v.CreatedAt),
	}
}

// decodeLimited reads a JSON body up to maxBody, so an oversized publish is a
// 400/413 instead of an unbounded allocation.
func decodeLimited(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	return nil
}
