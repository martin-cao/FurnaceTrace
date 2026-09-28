package protocol

import "time"

type BatchInput struct {
	BatchNo       string `json:"batchNo"`
	MaterialName  string `json:"materialName"`
	ProcessSpec   string `json:"processSpec"`
	QualityStatus string `json:"qualityStatus"`
	Enabled       *bool  `json:"enabled"`
	Notes         string `json:"notes"`
}
type BatchUpdate struct {
	BatchInput
	Version int64 `json:"version"`
}
type Batch struct {
	ID            string    `json:"id"`
	BatchNo       string    `json:"batchNo"`
	MaterialName  string    `json:"materialName"`
	ProcessSpec   string    `json:"processSpec"`
	QualityStatus string    `json:"qualityStatus"`
	Enabled       bool      `json:"enabled"`
	Notes         string    `json:"notes"`
	Version       int64     `json:"version"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	InUse         bool      `json:"inUse"`
}
type BasketInput struct {
	BasketNo string `json:"basketNo"`
	BatchID  string `json:"batchId"`
	Quantity int    `json:"quantity"`
	Enabled  *bool  `json:"enabled"`
	Notes    string `json:"notes"`
}
type BasketUpdate struct {
	BasketInput
	Version int64 `json:"version"`
}
type Basket struct {
	BasketNo      string    `json:"basketNo"`
	BatchID       string    `json:"batchId"`
	BatchNo       string    `json:"batchNo"`
	MaterialName  string    `json:"materialName"`
	QualityStatus string    `json:"qualityStatus"`
	BatchEnabled  bool      `json:"batchEnabled"`
	Quantity      int       `json:"quantity"`
	Enabled       bool      `json:"enabled"`
	Notes         string    `json:"notes"`
	Version       int64     `json:"version"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	InUse         bool      `json:"inUse"`
	Occupied      bool      `json:"occupied"`
}
type CatalogDelete struct {
	Version int64 `json:"version"`
}
type AdmissionSnapshot struct {
	BasketNo      string    `json:"basketNo"`
	BasketVersion int64     `json:"basketVersion"`
	BasketEnabled bool      `json:"basketEnabled"`
	BatchID       string    `json:"batchId"`
	BatchNo       string    `json:"batchNo"`
	BatchVersion  int64     `json:"batchVersion"`
	BatchEnabled  bool      `json:"batchEnabled"`
	MaterialName  string    `json:"materialName"`
	ProcessSpec   string    `json:"processSpec"`
	Quantity      int       `json:"quantity"`
	QualityStatus string    `json:"qualityStatus"`
	CheckedAt     time.Time `json:"checkedAt"`
}
