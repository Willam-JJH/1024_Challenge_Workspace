package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
	"challenge-api/internal/media"
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

// 一个素材最多允许的分片数，避免用海量小文件占满磁盘 inode
const maxAssetParts = 1000

// 允许原样下发的类型：其余一律按 application/octet-stream 处理，
// 避免把客户端声明的 text/html、image/svg+xml 之类的主动内容当网页给浏览器
var safeContentTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/gif":                true,
	"image/webp":               true,
	"application/pdf":          true,
	"application/zip":          true,
	"application/octet-stream": true,
	"text/plain":               true,
	"video/mp4":                true,
	"video/webm":               true,
	"video/quicktime":          true,
	"audio/mpeg":               true,
	"audio/ogg":                true,
	"audio/wav":                true,
}

func servedType(contentType string) string {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if safeContentTypes[base] {
		return base
	}
	return "application/octet-stream"
}

// collectParts 按分片序号排序；字典序会把 part10 排到 part2 前面。
func collectParts(base string) ([]string, error) {
	dir := filepath.Dir(base)
	prefix := filepath.Base(base) + ".part"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type indexed struct {
		index int
		path  string
	}
	found := make([]indexed, 0, 8)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		index, err := strconvAtoi(name[len(prefix):])
		if err != nil {
			continue
		}
		found = append(found, indexed{index, filepath.Join(dir, name)})
	}
	sort.Slice(found, func(left, right int) bool { return found[left].index < found[right].index })
	out := make([]string, len(found))
	for index, item := range found {
		out[index] = item.path
	}
	return out, nil
}

// partsSize 汇总已有分片大小（exclude 用来忽略正在覆盖写入的那一片）。
func partsSize(base, exclude string) int64 {
	dir := filepath.Dir(base)
	prefix := filepath.Base(base) + ".part"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == exclude {
			continue
		}
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
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
	// 配额按「总占用」算，在创建时就挡住，而不是等写盘失败才发现。
	if quota := s.cfg.UploadQuotaBytes; quota > 0 {
		var used int64
		if err := s.writePool().QueryRow(r.Context(),
			`SELECT COALESCE(sum(size), 0)::bigint FROM media_assets WHERE status <> 'removed'`).
			Scan(&used); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if used+int64(in.Size) > quota {
			writeError(w, r, http.StatusInsufficientStorage, "quota_exceeded", "素材存储空间已满")
			return
		}
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
	head := data
	if len(head) > 16 {
		head = head[:16]
	}
	contentType := sniffType(head, row.ContentType)

	// 图片先过派生管线：解不出来就直接拒绝，不要把坏图存进库。
	var (
		variantsJSON *string
		imageWidth   *int
		imageHeight  *int
	)
	if media.Processable(contentType) {
		result, err := media.Process(data)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "image_undecodable",
				"图片无法解析："+err.Error())
			return false
		}
		imageWidth, imageHeight = &result.Width, &result.Height
		if len(result.Renditions) > 0 {
			encoded, err := encodeVariants(row.ID, result.Renditions)
			if err != nil {
				WriteError(w, r, errInternal)
				return false
			}
			variantsJSON = &encoded
			if !s.writeRenditions(w, r, row.ID, result.Renditions) {
				return false
			}
		}
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
	row.ContentType = contentType
	row.Status = "ready"
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE media_assets SET size = $2, content_type = $3, status = 'ready',
			width = COALESCE($4, width), height = COALESCE($5, height),
			variants = COALESCE($6::jsonb, variants), updated_at = $7
		WHERE id = $1`,
		row.ID, row.Size, row.ContentType, imageWidth, imageHeight, variantsJSON,
		time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	return true
}

// encodeVariants 生成 media_assets.variants 的 JSON，形状与 Python 侧一致。
func encodeVariants(assetID string, renditions []media.Rendition) (string, error) {
	type variant struct {
		Key       string `json:"key"`
		Name      string `json:"name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		SizeBytes int    `json:"sizeBytes"`
	}
	list := make([]variant, 0, len(renditions))
	for _, rendition := range renditions {
		list = append(list, variant{
			Key:       assetID + "/" + rendition.Name + ".webp",
			Name:      rendition.Name,
			Width:     rendition.Width,
			Height:    rendition.Height,
			SizeBytes: len(rendition.Data),
		})
	}
	raw, err := json.Marshal(list)
	return string(raw), err
}

// writeRenditions 把各档派生图写到原文件所在目录。
func (s *Server) writeRenditions(w http.ResponseWriter, r *http.Request,
	assetID string, renditions []media.Rendition) bool {

	dir := filepath.Join(s.uploadRoot(), filepath.FromSlash(assetID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	for _, rendition := range renditions {
		path := filepath.Join(dir, rendition.Name+".webp")
		if err := os.WriteFile(path, rendition.Data, 0o644); err != nil {
			s.writeQueryError(w, r, err)
			return false
		}
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
	// 所有分片加起来也不能超过单文件上限，避免分片把磁盘占满
	if partsSize(base, filepath.Base(path))+int64(len(data)) > s.cfg.MaxUploadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "文件过大")
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
	parts, err := collectParts(base)
	if err != nil || len(parts) == 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "no_parts", "没有收到任何分片")
		return
	}
	if len(parts) > maxAssetParts {
		writeError(w, r, http.StatusUnprocessableEntity, "too_many_parts", "分片过多")
		return
	}
	var data []byte
	total := int64(0)
	for _, part := range parts {
		chunk, err := os.ReadFile(part)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		total += int64(len(chunk))
		if total > s.cfg.MaxUploadBytes {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "文件过大")
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
	// 原图和各档派生图都放在这个素材自己的目录里，整目录删掉。
	_ = os.RemoveAll(filepath.Dir(filepath.Join(s.uploadRoot(), filepath.FromSlash(row.StorageKey))))
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
	w.Header().Set("Content-Type", servedType(row.ContentType))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+strings.ReplaceAll(row.Filename, `"`, "")+`"`)
	http.ServeFile(w, r, path)
}

// variantNamePattern 只管形状，存在与否由文件系统回答。
// 收紧成固定形状是为了挡住 "../" 这类路径穿越。
var variantNamePattern = regexp.MustCompile(`^w[0-9]{1,5}$`)

func (s *Server) readAssetVariant(w http.ResponseWriter, r *http.Request) {
	row, ok := s.assetByID(w, r, r.PathValue("asset_id"))
	if !ok {
		return
	}
	if row.Status != "ready" {
		writeError(w, r, http.StatusNotFound, "not_found", "素材尚未完成上传")
		return
	}
	name := r.PathValue("variant")
	if !variantNamePattern.MatchString(name) {
		writeError(w, r, http.StatusNotFound, "variant_not_available",
			"变体 "+name+" 不存在")
		return
	}
	path := filepath.Join(s.uploadRoot(), filepath.FromSlash(row.ID), name+".webp")
	if _, err := os.Stat(path); err != nil {
		writeError(w, r, http.StatusNotFound, "variant_not_available",
			"变体 "+name+" 尚未生成")
		return
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
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
