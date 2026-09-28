package core

import (
	"bytes"
	"context"
	"encoding/json"
	"furnace.local/iot/internal/mes"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type catalogTest struct {
	t           *testing.T
	e           *Engine
	db          *pgxpool.Pool
	admin, user *http.Cookie
	handler     http.Handler
}

func newCatalogTest(t *testing.T, furnace string) *catalogTest {
	db := settingsDB(t)
	ctx := context.Background()
	e := &Engine{store: &Store{db: db}, cfg: []platform.FurnaceConfig{{ID: furnace, Name: "测试炉", Sensor: furnace + "-sensor", Door: furnace + "-door", Lamp: furnace + "-lamp", Temperature: furnace + "-temperature"}}, active: map[string]record{}, devices: map[string]protocol.DeviceView{}, deviceReceived: map[string]time.Time{}, cameras: map[string]protocol.CameraState{}, rules: map[string]*ruleRuntime{}, startup: time.Now().Add(-time.Minute)}
	for kind, id := range e.cfg[0].DeviceIDs() {
		e.devices[id] = protocol.DeviceView{DeviceID: id, FurnaceID: furnace, Kind: kind, Online: true, State: &protocol.DeviceState{DeviceID: id, FurnaceID: furnace, Kind: kind, BootID: protocol.ID(), Seq: 1, ObservedAt: time.Now(), Values: map[string]any{"temperatureC": 950.0, "doorClosed": true, "doorOpen": false, "present": false, "inFurnace": false}}}
		e.deviceReceived[id] = time.Now()
	}
	e.cameras[furnace] = protocol.CameraState{Online: true}
	if err := e.loadCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	seed := func(role string) *http.Cookie {
		id := protocol.ID()
		token := strings.ReplaceAll(protocol.ID()+protocol.ID(), "-", "")
		if _, err := db.Exec(ctx, "INSERT INTO core.users(id,username,display_name,password_hash,role) VALUES($1,$1,'test','unused',$2)", id, role); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO core.sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')", digestToken(token), id); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: "iot_session", Value: token}
	}
	c := &catalogTest{t: t, e: e, db: db, admin: seed("admin"), user: seed("user")}
	c.bind(e)
	return c
}
func (c *catalogTest) bind(e *Engine) {
	m := e.routes()
	e.catalogRoutes(m)
	e.alarmRuleRoutes(m)
	c.e = e
	c.handler = platform.Internal(e.authenticated(m, false))
}
func (c *catalogTest) request(method, path string, body any, key string, user bool) *httptest.ResponseRecorder {
	c.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Idempotency-Key", key)
	if user {
		r.AddCookie(c.user)
	} else {
		r.AddCookie(c.admin)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	return w
}
func (c *catalogTest) ok(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	w := c.request(method, path, body, protocol.ID(), false)
	if w.Code != 200 && w.Code != 202 {
		c.t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}
func responseID(w *httptest.ResponseRecorder) string {
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v.ID
}
func (c *catalogTest) batch(no, quality string) string {
	yes := true
	return responseID(c.ok("POST", "/api/v1/batches", protocol.BatchInput{BatchNo: no, MaterialName: "铝合金", ProcessSpec: "固溶处理", QualityStatus: quality, Enabled: &yes}))
}
func (c *catalogTest) basket(no, batch string) {
	yes := true
	c.ok("POST", "/api/v1/baskets", protocol.BasketInput{BasketNo: no, BatchID: batch, Quantity: 12, Enabled: &yes})
}
func (c *catalogTest) cycle(no string) record {
	now := time.Now().UTC()
	r := record{Cycle: protocol.Cycle{ID: protocol.ID(), FurnaceID: c.e.cfg[0].ID, BasketNo: no, Status: "VALIDATING", CreatedAt: now, UpdatedAt: now}}
	tx, err := c.db.Begin(context.Background())
	if err != nil {
		c.t.Fatal(err)
	}
	if err = saveRecord(context.Background(), tx, r); err != nil {
		c.t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		c.t.Fatal(err)
	}
	c.e.active[r.Cycle.FurnaceID] = r
	return r
}
func TestCatalogAdmissionCRUDAndSnapshot(t *testing.T) {
	c := newCatalogTest(t, "catalog-f1")
	ctx := context.Background()
	yes := true
	body := protocol.BatchInput{BatchNo: "CATALOG-01", MaterialName: "铝合金", ProcessSpec: "固溶", QualityStatus: "pending", Enabled: &yes}
	key := protocol.ID()
	w := c.request("POST", "/api/v1/batches", body, key, false)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	id := responseID(w)
	version := c.e.catalogVersion
	if second := c.request("POST", "/api/v1/batches", body, key, false); second.Code != 202 || responseID(second) != id || c.e.catalogVersion != version {
		t.Fatal("duplicate create was not idempotent")
	}
	if w = c.request("POST", "/api/v1/batches", body, protocol.ID(), true); w.Code != 403 {
		t.Fatal("reader could edit catalog")
	}
	c.basket("BAD0001", id)
	result, err := mes.Lookup(ctx, c.db, "MISSING", false)
	if err != nil || result.Result != 0 {
		t.Fatal("unknown basket was allowed")
	}
	result, err = mes.Lookup(ctx, c.db, "BAD0001", false)
	if err != nil || result.Reason != "QUALITY_PENDING" {
		t.Fatal("pending QC not rejected")
	}
	body.QualityStatus = "passed"
	c.ok("PUT", "/api/v1/batches/"+id, protocol.BatchUpdate{BatchInput: body, Version: 1})
	if w = c.request("PUT", "/api/v1/batches/"+id, protocol.BatchUpdate{BatchInput: body, Version: 1}, protocol.ID(), false); w.Code != 409 {
		t.Fatal("stale edit overwrote data")
	}
	result, err = mes.Lookup(ctx, c.db, "BAD0001", false)
	if err != nil || result.Result != 1 {
		t.Fatal("prefix still controlled admission")
	}
	if w = c.request("DELETE", "/api/v1/batches/"+id, protocol.CatalogDelete{Version: 2}, protocol.ID(), false); w.Code != 409 {
		t.Fatal("deleted batch with baskets")
	}
	w = c.request("GET", "/api/v1/baskets/BAD0001/qrcode", nil, "", true)
	img, err := png.Decode(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	bitmap, _ := gozxing.NewBinaryBitmapFromImage(img)
	qr, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil || qr.GetText() != "BAD0001" {
		t.Fatal("invalid QR label")
	}
	r := c.cycle("BAD0001")
	if w = c.request("PUT", "/api/v1/batches/"+id, protocol.BatchUpdate{BatchInput: body, Version: 2}, protocol.ID(), false); w.Code != 409 {
		t.Fatal("active batch edited")
	}
	// Simulate a change between the remote MES result and local reservation.
	if _, err = c.db.Exec(ctx, "UPDATE core.batches SET quality_status='failed',version=version+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = c.e.acceptMES(ctx, r, result); err != nil {
		t.Fatal(err)
	}
	r = c.e.active[r.Cycle.FurnaceID]
	if r.Cycle.Status != "BLOCKED" || r.Cycle.MES.Reason != "QUALITY_FAILED" {
		t.Fatal("stale MES verdict opened door")
	}
	var commands int
	c.db.QueryRow(ctx, "SELECT count(*) FROM core.commands WHERE cycle_id=$1", r.Cycle.ID).Scan(&commands)
	if commands != 0 {
		t.Fatal("rejected cycle issued commands")
	}
	if err = c.e.transition(ctx, r, "ABORTED", "test cleanup", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = c.db.Exec(ctx, "UPDATE core.batches SET quality_status='passed',version=version+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	result, _ = mes.Lookup(ctx, c.db, "BAD0001", false)
	r = c.cycle("BAD0001")
	if err = c.e.acceptMES(ctx, r, result); err != nil {
		t.Fatal(err)
	}
	r = c.e.active[r.Cycle.FurnaceID]
	if r.Cycle.Status != "OPENING" || r.Cycle.MES.Snapshot.MaterialName != "铝合金" {
		t.Fatal("catalog-approved cycle did not start")
	}
	if _, err = c.db.Exec(ctx, "UPDATE core.batches SET material_name='修改后的物料',version=version+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	c.db.QueryRow(ctx, "SELECT doc FROM core.cycles WHERE id=$1", r.Cycle.ID).Scan(&raw)
	var saved record
	json.Unmarshal(raw, &saved)
	if saved.Cycle.MES.Snapshot.MaterialName != "铝合金" {
		t.Fatal("historical snapshot changed with master data")
	}
	if err = c.e.transition(ctx, r, "ABORTED", "test cleanup", ""); err != nil {
		t.Fatal(err)
	}
	if w = c.request("DELETE", "/api/v1/baskets/BAD0001", protocol.CatalogDelete{Version: 1}, protocol.ID(), false); w.Code != 409 {
		t.Fatal("occupied basket deleted")
	}
	empty := c.batch("DELETE-01", "passed")
	c.basket("DEL0001", empty)
	c.ok("DELETE", "/api/v1/baskets/DEL0001", protocol.CatalogDelete{Version: 1})
	if w = c.request("GET", "/api/v1/baskets/DEL0001", nil, "", false); w.Code != 404 {
		t.Fatal("deleted basket still visible")
	}
	c.ok("DELETE", "/api/v1/batches/"+empty, protocol.CatalogDelete{Version: 1})
	var retained bool
	c.db.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM core.baskets WHERE basket_no='DEL0001'").Scan(&retained)
	if !retained {
		t.Fatal("delete erased lineage")
	}
}
func TestRuleConditionsANDORAndUnknown(t *testing.T) {
	enabled := true
	clearOverlap := ruleLeaf("temperatureC", "gt", 50)
	overlap := Engine{rules: map[string]*ruleRuntime{"r": {Rule: protocol.AlarmRule{AlarmRuleInput: protocol.AlarmRuleInput{Enabled: &enabled, FurnaceID: "x", Condition: ruleLeaf("temperatureC", "gt", 100), ClearCondition: &clearOverlap, Channels: &protocol.RuleChannels{}}, ID: "r"}, Alarm: &protocol.Alarm{ID: "a"}}}, devices: map[string]protocol.DeviceView{"t": {Online: true, State: &protocol.DeviceState{Values: map[string]any{"temperatureC": 200.0}}}}, deviceReceived: map[string]time.Time{"t": time.Now()}}
	overlap.monitorRules(context.Background(), platform.FurnaceConfig{ID: "x", Temperature: "t"}, time.Now())
	if overlap.rules["r"].Alarm == nil {
		t.Fatal("overlapping trigger/clear oscillated")
	}

	e := Engine{active: map[string]record{"normal": {Cycle: protocol.Cycle{Status: "RESETTING", Reason: "正常复位"}}}}
	f := platform.FurnaceConfig{ID: "normal"}
	if e.ruleValues(f)["workflowFault"].value != false {
		t.Fatal("normal reset was treated as a fault")
	}
	r := e.active["normal"]
	r.ResetAborts = true
	r.AlarmReason = "DEVICE_OFFLINE"
	e.active["normal"] = r
	if e.ruleValues(f)["workflowFault"].value != true {
		t.Fatal("manual reset cleared fault before feedback")
	}

	c := ruleGroup("AND", ruleGroup("OR", ruleLeaf("temperatureC", "gt", 800), ruleLeaf("temperatureC", "lt", 600)), ruleLeaf("doorClosed", "eq", true))
	leaves := 0
	if !validCondition(c, 1, &leaves) {
		t.Fatal("valid nested rule rejected")
	}
	values := map[string]ruleValue{"temperatureC": {900.0, true}, "doorClosed": {true, true}}
	if evalCondition(c, values) != yes {
		t.Fatal("AND/OR expression failed")
	}
	values["temperatureC"] = ruleValue{700.0, true}
	if evalCondition(c, values) != no {
		t.Fatal("normal range alarmed")
	}
	values["temperatureC"] = ruleValue{}
	if evalCondition(c, values) != unknown {
		t.Fatal("missing reading treated as a value")
	}
	bad := ruleLeaf("doorOpen", "gt", 20)
	leaves = 0
	if validCondition(bad, 1, &leaves) {
		t.Fatal("invalid boolean comparison accepted")
	}
}
func TestRuleMigrationReminderRestartAndRecovery(t *testing.T) {
	c := newCatalogTest(t, "rules-f1")
	ctx := context.Background()
	yes, no := true, false
	if _, err := c.db.Exec(ctx, `CREATE TABLE core.temperature_alarm_settings(furnace_id text PRIMARY KEY,settings jsonb NOT NULL); INSERT INTO core.temperature_alarm_settings VALUES('rules-f1','{"upperLimitC":900,"recoveryDeltaC":5}')`); err != nil {
		t.Fatal(err)
	}
	legacy := protocol.Alarm{ID: protocol.ID(), FurnaceID: "rules-f1", Code: "TEMPERATURE_HIGH", Message: "旧炉温报警", RaisedAt: time.Now().UTC()}
	if _, err := c.db.Exec(ctx, "INSERT INTO core.alarms(id,furnace_id,cycle_id,code,raised_at,doc) VALUES($1,$2,'',$3,$4,$5)", legacy.ID, legacy.FurnaceID, legacy.Code, legacy.RaisedAt, encoded(legacy)); err != nil {
		t.Fatal(err)
	}
	if err := c.e.loadAlarmRules(ctx); err != nil {
		t.Fatal(err)
	}
	var temperature *ruleRuntime
	for _, r := range c.e.rules {
		if r.Rule.Category == "temperature" {
			temperature = r
		}
	}
	if temperature == nil || temperature.Alarm == nil || temperature.Alarm.ID != legacy.ID || !temperature.Notified || string(temperature.Rule.Condition.Value) != "900" {
		t.Fatal("legacy alarm/threshold not adopted")
	}
	clear := ruleLeaf("temperatureC", "lte", 980)
	in := protocol.AlarmRuleInput{Name: "组合报警", FurnaceID: "rules-f1", Category: "rules", Enabled: &yes, Condition: ruleGroup("AND", ruleLeaf("temperatureC", "gt", 1000), ruleLeaf("doorClosed", "eq", true)), ClearCondition: &clear, Channels: &protocol.RuleChannels{Telegram: true, Lamp: false}, RepeatSeconds: 10, Recoveries: true}
	id := responseID(c.ok("POST", "/api/v1/alarm-rules", in))
	now := time.Now().UTC()
	tempID := c.e.cfg[0].Temperature
	c.e.devices[tempID].State.Values["temperatureC"] = 1050.0
	c.e.monitorRules(ctx, c.e.cfg[0], now)
	rt := c.e.rules[id]
	if rt.Alarm == nil || !rt.Notified || rt.Next == nil || rt.Alarm.Lamp == nil || *rt.Alarm.Lamp {
		t.Fatal("rule did not activate with chosen channels")
	}
	alarmID := rt.Alarm.ID
	count := func(kind string) int {
		var n int
		c.db.QueryRow(ctx, "SELECT count(*) FROM core.outbox WHERE kind='notification' AND payload->>'alarmId'=$1 AND payload->>'kind'=$2", alarmID, kind).Scan(&n)
		return n
	}
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(time.Second))
	if count("ALARM") != 1 || count("REMINDER") != 0 {
		t.Fatal("duplicate first notification")
	}
	offline := c.e.devices[tempID]
	offline.Online = false
	c.e.devices[tempID] = offline
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(2*time.Second))
	if c.e.rules[id].Alarm == nil {
		t.Fatal("stale temperature falsely recovered")
	}
	offline.Online = true
	c.e.devices[tempID] = offline
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(11*time.Second))
	if count("REMINDER") != 1 {
		t.Fatal("missing scheduled reminder")
	}
	restarted := &Engine{store: c.e.store, cfg: c.e.cfg, active: c.e.active, devices: c.e.devices, deviceReceived: c.e.deviceReceived, cameras: c.e.cameras, startup: c.e.startup, catalogVersion: c.e.catalogVersion}
	if err := restarted.loadAlarmRules(ctx); err != nil {
		t.Fatal(err)
	}
	c.bind(restarted)
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(12*time.Second))
	if count("ALARM") != 1 || count("REMINDER") != 1 {
		t.Fatal("restart replayed first alert or reminder")
	}
	if _, err := c.db.Exec(ctx, "UPDATE core.alarms SET doc=jsonb_set(doc,'{acknowledgedAt}',to_jsonb(now())) WHERE id=$1", alarmID); err != nil {
		t.Fatal(err)
	}
	c.e.devices[tempID].State.Values["temperatureC"] = 950.0
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(13*time.Second))
	if c.e.rules[id].Alarm != nil || count("RECOVERY") != 1 {
		t.Fatal("rule recovery not persisted")
	}
	var raw []byte
	c.db.QueryRow(ctx, "SELECT doc FROM core.alarms WHERE id=$1", alarmID).Scan(&raw)
	var a protocol.Alarm
	json.Unmarshal(raw, &a)
	if a.AcknowledgedAt == nil {
		t.Fatal("recovery overwrote acknowledgement")
	}
	var cancelled int
	c.db.QueryRow(ctx, "SELECT count(*) FROM core.outbox WHERE kind='cancel-reminders' AND payload->>'alarmId'=$1", alarmID).Scan(&cancelled)
	if cancelled != 1 {
		t.Fatal("queued reminders not cancelled on recovery")
	}
	preset := c.e.rules[temperature.Rule.ID]
	update := preset.Rule.AlarmRuleInput
	update.Enabled = &no
	c.ok("PUT", "/api/v1/alarm-rules/"+preset.Rule.ID, protocol.AlarmRuleUpdate{AlarmRuleInput: update, Version: preset.Rule.Version})
	c.e.devices[tempID].State.Values["temperatureC"] = 960.0
	c.e.monitorRules(ctx, c.e.cfg[0], now.Add(time.Hour))
	if c.e.rules[preset.Rule.ID].Alarm != nil {
		t.Fatal("old hardcoded temperature alarm still active")
	}
	var oldTable bool
	c.db.QueryRow(ctx, "SELECT to_regclass('core.temperature_alarm_settings') IS NOT NULL").Scan(&oldTable)
	if oldTable {
		t.Fatal("legacy temperature config was not retired")
	}
}
