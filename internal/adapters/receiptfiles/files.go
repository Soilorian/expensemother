package receiptfiles

import (
	"bytes"
	"context"
	app "expensemother/internal/application"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"time"
)

const Limit = 10 << 20

type Files struct {
	Root    string
	Service *app.Service
}

func (f *Files) Save(ctx context.Context, u, g string, input io.Reader) (*re.Receipt, error) {
	b, e := io.ReadAll(io.LimitReader(input, Limit+1))
	if e != nil {
		return nil, e
	}
	if len(b) > Limit {
		return nil, &shared.Error{Status: 413, Code: "IMAGE_TOO_LARGE", Message: "Maximum image size is 10 MiB"}
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 30000000 {
		return nil, &shared.Error{Status: 415, Code: "INVALID_IMAGE", Message: "Use a JPEG, PNG or WebP receipt under 30 megapixels"}
	}
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}[format]
	if mime == "" {
		return nil, shared.Bad("unsupported image")
	}
	if e = os.MkdirAll(f.Root, 0700); e != nil {
		return nil, e
	}
	v := &re.Receipt{ID: shared.ID(), GroupID: g, Owner: u, ContentType: mime, CreatedAt: time.Now().UTC()}
	v.Path = filepath.Join(f.Root, v.ID+"."+format)
	if e = os.WriteFile(v.Path, b, 0600); e != nil {
		return nil, e
	}
	if e = f.Service.SaveReceipt(ctx, u, v); e != nil {
		_ = os.Remove(v.Path)
		return nil, e
	}
	return v, nil
}
func (f *Files) Open(ctx context.Context, u, id string) (*re.Receipt, *os.File, error) {
	v, e := f.Service.Receipt(ctx, u, id)
	if e != nil {
		return nil, nil, e
	}
	root, e := filepath.Abs(f.Root)
	if e != nil {
		return nil, nil, e
	}
	path, e := filepath.Abs(v.Path)
	if e != nil {
		return nil, nil, e
	}
	rel, e := filepath.Rel(root, path)
	if e != nil || rel == ".." || filepath.IsAbs(rel) || filepath.Dir(rel) != "." {
		return nil, nil, shared.Forbidden()
	}
	file, e := os.Open(path)
	return v, file, e
}
func Public(v *re.Receipt) map[string]any {
	return map[string]any{"receiptId": v.ID, "groupId": nullable(v.GroupID), "expenseId": nullable(v.ExpenseID), "uploadedByUserId": v.Owner, "contentType": v.ContentType, "createdAt": v.CreatedAt}
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
