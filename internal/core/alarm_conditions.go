package core

import (
	"encoding/json"
	"fmt"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"math"
	"strings"
	"time"
)

type truth int

const (
	unknown truth = -1
	no      truth = 0
	yes     truth = 1
)

type ruleValue struct {
	value any
	valid bool
}

var ruleFields = map[string]string{"temperatureC": "炉温", "entryPresent": "入口到位", "inFurnace": "炉内到位", "doorOpen": "炉门已开", "doorClosed": "炉门已关", "sensorOnline": "到位传感器在线", "doorOnline": "炉门执行器在线", "lampOnline": "报警器在线", "temperatureOnline": "炉温传感器在线", "cameraOnline": "摄像头在线", "scanFailed": "扫码失败", "mesRejected": "MES 拒绝入炉", "mesUnavailable": "MES 通信失败", "doorTimeout": "开关门超时", "entryTimeout": "入炉到位超时", "resetTimeout": "复位超时", "workflowFault": "入炉执行异常待处理"}

func validCondition(c protocol.RuleCondition, depth int, leaves *int) bool {
	if depth > 4 {
		return false
	}
	if c.Type == "group" {
		if c.Field != "" || c.Operator != "" || len(c.Value) > 0 || (c.Logic != "AND" && c.Logic != "OR") || len(c.Children) == 0 || len(c.Children) > 16 {
			return false
		}
		for _, child := range c.Children {
			if !validCondition(child, depth+1, leaves) {
				return false
			}
		}
		return true
	}
	if c.Type != "condition" || c.Logic != "" || len(c.Children) > 0 || ruleFields[c.Field] == "" {
		return false
	}
	*leaves++
	if *leaves > 16 {
		return false
	}
	var value any
	if json.Unmarshal(c.Value, &value) != nil {
		return false
	}
	if c.Field == "temperatureC" {
		v, ok := value.(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
		switch c.Operator {
		case "gt", "gte", "lt", "lte", "eq", "ne":
			return true
		}
		return false
	}
	_, ok := value.(bool)
	return ok && (c.Operator == "eq" || c.Operator == "ne")
}
func evalCondition(c protocol.RuleCondition, values map[string]ruleValue) truth {
	if c.Type == "group" {
		missing := false
		for _, child := range c.Children {
			v := evalCondition(child, values)
			if c.Logic == "AND" && v == no {
				return no
			}
			if c.Logic == "OR" && v == yes {
				return yes
			}
			missing = missing || v == unknown
		}
		if missing {
			return unknown
		}
		if c.Logic == "AND" {
			return yes
		}
		return no
	}
	v := values[c.Field]
	if !v.valid {
		return unknown
	}
	var target any
	if json.Unmarshal(c.Value, &target) != nil {
		return unknown
	}
	matched := false
	if n, ok := v.value.(float64); ok {
		want, ok := target.(float64)
		if !ok {
			return unknown
		}
		switch c.Operator {
		case "gt":
			matched = n > want
		case "gte":
			matched = n >= want
		case "lt":
			matched = n < want
		case "lte":
			matched = n <= want
		case "eq":
			matched = n == want
		case "ne":
			matched = n != want
		}
	} else {
		a, ok := v.value.(bool)
		b, other := target.(bool)
		if !ok || !other {
			return unknown
		}
		matched = a == b
		if c.Operator == "ne" {
			matched = !matched
		}
	}
	if matched {
		return yes
	}
	return no
}
func describeCondition(c protocol.RuleCondition, values map[string]ruleValue) string {
	if c.Type == "group" {
		parts := []string{}
		for _, v := range c.Children {
			parts = append(parts, describeCondition(v, values))
		}
		return "(" + strings.Join(parts, " "+c.Logic+" ") + ")"
	}
	symbol := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<=", "eq": "=", "ne": "!="}[c.Operator]
	reading := "无新数据"
	if values[c.Field].valid {
		reading = fmt.Sprint(values[c.Field].value)
	}
	return ruleFields[c.Field] + " " + reading + " " + symbol + " " + string(c.Value)
}
func (e *Engine) ruleValues(f platform.FurnaceConfig) map[string]ruleValue {
	out := map[string]ruleValue{}
	for kind, id := range f.DeviceIDs() {
		v := e.device(id)
		onlineField := map[string]string{"sensor": "sensorOnline", "door": "doorOnline", "lamp": "lampOnline", "temperature": "temperatureOnline"}[kind]
		out[onlineField] = ruleValue{v.Online, v.Online || time.Since(e.startup) > 10*time.Second}
		if !v.Online || v.State == nil {
			continue
		}
		fields := map[string][]string{"sensor": {"present", "inFurnace"}, "door": {"doorOpen", "doorClosed"}, "temperature": {"temperatureC"}}
		for _, field := range fields[kind] {
			name := field
			if name == "present" {
				name = "entryPresent"
			}
			raw, ok := v.State.Values[field]
			if field == "temperatureC" {
				n, valid := raw.(float64)
				ok = ok && valid && !math.IsNaN(n) && !math.IsInf(n, 0)
			} else {
				_, valid := raw.(bool)
				ok = ok && valid
			}
			out[name] = ruleValue{raw, ok}
		}
	}
	camera := e.cameras[f.ID]
	out["cameraOnline"] = ruleValue{camera.Online, camera.Online || time.Since(e.startup) > 10*time.Second}
	reason := ""
	stage := ""
	resettingFault := false
	if r, ok := e.active[f.ID]; ok {
		reason = r.Cycle.Reason
		stage = r.Cycle.Status
		resettingFault = r.ResetAborts && (stage == "RESETTING" || stage == "CLOSING")
		if resettingFault && r.AlarmReason != "" {
			reason = r.AlarmReason
		}
	}
	flags := map[string]bool{
		"scanFailed":     reason == "SCAN_TIMEOUT" || reason == "INVALID_BARCODE" || reason == "AMBIGUOUS",
		"mesRejected":    reason == "MES_REJECTED" || reason == "BASKET_OCCUPIED",
		"mesUnavailable": reason == "MES_UNAVAILABLE",
		"doorTimeout":    reason == "OPEN_TIMEOUT" || reason == "CLOSE_TIMEOUT",
		"entryTimeout":   reason == "ENTRY_TIMEOUT", "resetTimeout": reason == "RESET_TIMEOUT",
	}
	known := false
	for name, v := range flags {
		out[name] = ruleValue{v, true}
		known = known || v
	}
	faultStage := stage == "BLOCKED" || stage == "NEEDS_INPUT" || resettingFault
	out["workflowFault"] = ruleValue{faultStage && !known && reason != "CAMERA_OFFLINE" && reason != "人工复位", true}
	return out
}

func usesWorkflowCondition(c protocol.RuleCondition) bool {
	switch c.Field {
	case "scanFailed", "mesRejected", "mesUnavailable", "doorTimeout", "entryTimeout", "resetTimeout", "workflowFault":
		return true
	}
	for _, child := range c.Children {
		if usesWorkflowCondition(child) {
			return true
		}
	}
	return false
}
