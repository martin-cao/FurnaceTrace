package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"time"
)

//go:embed schema.sql
var schema string

type record struct {
	AlarmReason        string         `json:"alarmReason,omitempty"`
	Cycle              protocol.Cycle `json:"cycle"`
	Deadline           time.Time      `json:"deadline"`
	SessionID          string         `json:"sessionId"`
	CommandID          string         `json:"commandId"`
	ValidationAttempts int            `json:"validationAttempts"`
	NextValidation     time.Time      `json:"nextValidation"`
	ResetAborts        bool           `json:"resetAborts"`
}
type Store struct {
	db   *pgxpool.Pool
	lock *pgxpool.Conn
}

func openStore(ctx context.Context) (*Store, error) {
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(ctx, schema); err != nil {
		db.Close()
		return nil, err
	}
	lock, err := db.Acquire(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	var acquired bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(7614862)").Scan(&acquired); err != nil || !acquired {
		lock.Release()
		db.Close()
		return nil, errors.New("another workflow controller owns the database")
	}
	return &Store{db: db, lock: lock}, nil
}
func (s *Store) close() {
	if s.lock != nil {
		s.lock.Release()
	}
	s.db.Close()
}
func encoded(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func saveRecord(ctx context.Context, tx pgx.Tx, r record) error {
	_, err := tx.Exec(ctx, `INSERT INTO core.cycles(id,furnace_id,basket_no,status,created_at,doc) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(id) DO UPDATE SET basket_no=excluded.basket_no,status=excluded.status,doc=excluded.doc`, r.Cycle.ID, r.Cycle.FurnaceID, r.Cycle.BasketNo, r.Cycle.Status, r.Cycle.CreatedAt, encoded(r))
	return err
}
func event(ctx context.Context, tx pgx.Tx, cycle, kind, message string) error {
	v := protocol.CycleEvent{ID: protocol.ID(), CycleID: cycle, Kind: kind, Message: message, At: time.Now().UTC()}
	_, err := tx.Exec(ctx, "INSERT INTO core.events(id,cycle_id,at,doc) VALUES($1,$2,$3,$4)", v.ID, cycle, v.At, encoded(v))
	return err
}
func enqueue(ctx context.Context, tx pgx.Tx, id, kind string, payload any) error {
	_, err := tx.Exec(ctx, "INSERT INTO core.outbox(id,kind,payload) VALUES($1,$2,$3) ON CONFLICT(id) DO NOTHING", id, kind, encoded(payload))
	return err
}
func notify(ctx context.Context, tx pgx.Tx, a protocol.Alarm, kind string) error {
	label := "报警"
	if kind == "RECOVERY" {
		label = "恢复"
	}
	n := protocol.NotificationRequest{ID: protocol.ID(), AlarmID: a.ID, FurnaceID: a.FurnaceID, Kind: kind, Category: notificationCategory(a.Code), Text: "[入炉" + label + "] " + a.FurnaceID + "\n" + a.Code + ": " + a.Message + "\n报警编号: " + a.ID, CreatedAt: time.Now().UTC()}
	if a.Category != "" {
		n.Category = a.Category
	}
	if kind == "RECOVERY" {
		n.Text = "[入炉恢复] " + a.FurnaceID + "\n状态：已恢复\n原报警：" + a.Code + " — " + a.Message + "\n报警编号: " + a.ID
	}
	if kind == "REMINDER" {
		n.Text = "[持续报警提醒] " + a.FurnaceID + "\n故障尚未确认恢复\n原报警：" + a.Message + "\n发生时间：" + a.RaisedAt.Format(time.RFC3339) + "\n报警编号: " + a.ID
	}
	if kind == "RECOVERY" && (a.Resolution == "rule_disabled" || a.Resolution == "rule_deleted") {
		n.Text = "[报警规则结束] " + a.FurnaceID + "\n规则已停用或删除，停止后续提醒；这不表示现场故障已消除。\n原报警：" + a.Message + "\n报警编号: " + a.ID
	}
	return enqueue(ctx, tx, n.ID, "notification", n)
}
func (s *Store) active(ctx context.Context) (map[string]record, error) {
	rows, err := s.db.Query(ctx, "SELECT doc FROM core.cycles WHERE status NOT IN ('COMPLETED','ABORTED')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]record{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var r record
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out[r.Cycle.FurnaceID] = r
	}
	return out, rows.Err()
}
