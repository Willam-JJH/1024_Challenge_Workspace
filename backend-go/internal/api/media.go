package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	assetCreatePattern   = "POST /api/v1/media/assets"
	assetContentPattern  = "PUT /api/v1/media/assets/{asset_id}/content"
	assetPartPattern     = "PUT /api/v1/media/assets/{asset_id}/parts/{part}"
	assetCompletePattern = "POST /api/v1/media/assets/{asset_id}/completions"
	assetGetPattern      = "GET /api/v1/media/assets/{asset_id}"
	assetPatchPattern    = "PATCH /api/v1/media/assets/{asset_id}"
	assetDeletePattern   = "DELETE /api/v1/media/assets/{asset_id}"
	fileReadPattern      = "GET /api/v1/files/{asset_id}"
	fileVariantPattern   = "GET /api/v1/files/{asset_id}/{variant}"
)

type assetOut struct {
	ID          string  `json:"id"`
	Filename    string  `json:"filename"`
	ContentType string  `json:"content_type"`
	Size        int     `json:"size"`
	Purpose     string  `json:"purpose"`
	Alt         *string `json:"alt"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
}

type assetCreatedOut struct {
	Asset     assetOut `json:"asset"`
	UploadURL string   `json:"upload_url"`
}

var allowedPurposes = map[string]bool{
	"submission": true, "avatar": true, "sponsor_logo": true, "pitch": true,
}

type assetRow struct {
	ID          string
	OwnerID     string
	Filename    string
	ContentType string
	Size        int
	Purpose     string
	Alt         pgtype.Text
	Status      string
	StorageKey  string
	CreatedAt   time.Time
}

func (a *assetRow) out() assetOut {
	return assetOut{
		ID:          a.ID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		Size:        a.Size,
		Purpose:     a.Purpose,
		Alt:         textPtr(a.Alt),
		Status:      a.Status,
		CreatedAt:   formatTime(a.CreatedAt),
	}
}

func safeFilename(name string) string {
	base := filepath.Base(name)
	var builder strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	cleaned := strings.Trim(builder.String(), "._")
	if cleaned == "" {
		cleaned = "file"
	}
	if len(cleaned) > 120 {
		cleaned = cleaned[:120]
	}
	return cleaned
}

func (s *Server) uploadRoot() string {
	root := s.cfg.UploadDir
	if root == "" {
		root = "uploads"
	}
	_ = os.MkdirAll(root, 0o755)
	return root
}

func (s *Server) assetByID(w http.ResponseWriter, r *http.Request,
	assetID string) (*assetRow, bool) {
	var row assetRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, owner_id, filename, content_type, size, purpose, alt, status,
			storage_key, created_at
		FROM media_assets WHERE id = $1`, assetID).
		Scan(&row.ID, &row.OwnerID, &row.Filename, &row.ContentType, &row.Size, &row.Purpose,
			&row.Alt, &row.Status, &row.StorageKey, &row.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "素材不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if row.Status == "removed" {
		writeError(w, r, http.StatusNotFound, "not_found", "素材不存在")
		return nil, false
	}
	return &row, true
}

func (s *Server) ownAsset(w http.ResponseWriter, r *http.Request, row *assetRow,
	principal *auth.Principal) bool {
	if row.OwnerID == principal.User.ID || principal.User.Role == "staff" ||
		principal.User.Role == "admin" {
		return true
	}
	writeError(w, r, http.StatusForbidden, "forbidden", "只能操作自己的上传")
	return false
}

func sniffType(head []byte, declared string) string {
	switch {
	case strings.HasPrefix(string(head), "\x89PNG\r\n\x1a\n"):
		return "image/png"
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case strings.HasPrefix(string(head), "GIF8"):
		return "image/gif"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "image/webp"
	case strings.HasPrefix(string(head), "%PDF"):
		return "application/pdf"
	}
	if declared == "" {
		return "application/octet-stream"
	}
	return declared
}

type assetCreateIn struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Purpose     string `json:"purpose"`
}

func (s *Server) createAsset(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in assetCreateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ContentType == "" {
		in.ContentType = "application/octet-stream"
	}
	if in.Purpose == "" {
		in.Purpose = "submission"
	}
	if len([]rune(in.Filename)) < 1 || len([]rune(in.Filename)) > 200 ||
		len(in.ContentType) > 120 || in.Size < 0 || !allowedPurposes[in.Purpose] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	filename := safeFilename(in.Filename)
	storageKey := id + "/" + filename
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO media_assets (id, owner_id, filename, content_type, size, purpose, alt,
			status, storage_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, 'pending', $7, $8, $8)`,
		id, principal.User.ID, filename, in.ContentType, in.Size, in.Purpose, storageKey,
		now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, assetCreatedOut{
		Asset: assetOut{
			ID:          id,
			Filename:    filename,
			ContentType: in.ContentType,
			Size:        in.Size,
			Purpose:     in.Purpose,
			Status:      "pending",
			CreatedAt:   formatTime(now),
		},
		UploadURL: "/api/v1/media/assets/" + id + "/content",
	})
}

// storeBytes 写入文件并更新元数据，复刻 Python 的 _write。
func (s *Server) storeBytes(w http.ResponseWriter, r *http.Request, row *assetRow,
	data []byte) bool {
	if len(data) == 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "empty_body", "内容为空")
		return false
	}
	if int64(len(data)) > s.cfg.MaxUploadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "文件过大")
		return false
	}
	path := filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	row.Size = len(data)
	head := data
	if len(head) > 16 {
		head = head[:16]
	}
	row.ContentType = sniffType(head, row.ContentType)
	row.Status = "ready"
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE media_assets SET size = $2, content_type = $3, status = 'ready', updated_at = $4
		WHERE id = $1`, row.ID, row.Size, row.ContentType, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	return true
}

func (s *Server) uploadAssetContent(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if !s.ownAsset(w, r, row, principal) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxUploadBytes+1))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !s.storeBytes(w, r, row, data) {
		return
	}
	writeJSON(w, http.StatusOK, row.out())
}

func (s *Server) uploadAssetPart(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if !s.ownAsset(w, r, row, principal) {
		return
	}
	part, err := strconvAtoi(r.PathValue("part"))
	if err != nil || part < 1 || part > 10000 {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_part", "分片序号不合法")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxUploadBytes+1))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if int64(len(data)) > s.cfg.MaxUploadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "分片过大")
		return
	}
	base := filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey))
	path := base + ".part" + strconvItoa(part)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) completeAsset(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if !s.ownAsset(w, r, row, principal) {
		return
	}
	base := filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey))
	parts, err := filepath.Glob(base + ".part*")
	if err != nil || len(parts) == 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "no_parts", "没有收到任何分片")
		return
	}
	sort.Strings(parts)
	var data []byte
	for _, part := range parts {
		chunk, err := os.ReadFile(part)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		data = append(data, chunk...)
	}
	if !s.storeBytes(w, r, row, data) {
		return
	}
	for _, part := range parts {
		_ = os.Remove(part)
	}
	writeJSON(w, http.StatusOK, row.out())
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, row.out())
}

type altIn struct {
	Alt *string `json:"alt"`
}

func (s *Server) patchAsset(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if !s.ownAsset(w, r, row, principal) {
		return
	}
	var in altIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Alt != nil && len([]rune(*in.Alt)) > 300 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE media_assets SET alt = $2, updated_at = $3 WHERE id = $1`,
		row.ID, in.Alt, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.Alt = pgtype.Text{}
	if in.Alt != nil {
		row.Alt = pgtype.Text{String: *in.Alt, Valid: true}
	}
	writeJSON(w, http.StatusOK, row.out())
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if !s.ownAsset(w, r, row, principal) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE media_assets SET status = 'removed', updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	_ = os.Remove(filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey)))
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) readAssetFile(w http.ResponseWriter, r *http.Request) {
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if row.Status != "ready" {
		writeError(w, r, http.StatusNotFound, "not_found", "素材尚未完成上传")
		return
	}
	path := filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey))
	if _, err := os.Stat(path); err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "文件名下的内容不存在")
		return
	}
	w.Header().Set("Content-Type", row.ContentType)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+strings.ReplaceAll(row.Filename, `"`, "")+`"`)
	http.ServeFile(w, r, path)
}

func (s *Server) readAssetVariant(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.assetByID(w, r, r.PathValue("asset_id")); !ok {
		return
	}
	writeError(w, r, http.StatusNotFound, "variant_not_available",
		"变体 "+r.PathValue("variant")+" 尚未生成（图片处理管线待接入）")
}

func strconvAtoi(value string) (int, error) {
	result := 0
	if value == "" {
		return 0, errInvalidNumber
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errInvalidNumber
		}
		result = result*10 + int(r-'0')
	}
	return result, nil
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

var errInvalidNumber = &auth.Problem{Status: 422, Code: "unprocessable", Message: "数字格式不正确"}
