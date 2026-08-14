package main

import (
	"errors"
	"strings"
	"testing"
)

func TestAbortUnknownTools(t *testing.T) {
	if abort, _ := abortUnknownTools(2, 5); abort {
		t.Error("2/5 不应中止")
	}
	if abort, msg := abortUnknownTools(5, 5); !abort || !strings.Contains(msg, "5") {
		t.Errorf("5/5 应中止, got abort=%v msg=%q", abort, msg)
	}
	if abort, _ := abortUnknownTools(8, 5); !abort {
		t.Error("8/5 应中止")
	}
	if abort, _ := abortUnknownTools(0, 0); !abort {
		t.Error("0/0 应中止")
	}
}

func TestParseErrMessage(t *testing.T) {
	msg := parseErrMessage(errors.New("bad json"))
	if !strings.Contains(msg, "bad json") || !strings.Contains(msg, "action") {
		t.Errorf("消息应含错误与参数说明: %q", msg)
	}
}

func TestRepeatCallMessage(t *testing.T) {
	msg := repeatCallMessage("hash1", 3)
	if !strings.Contains(msg, "3") {
		t.Errorf("应含次数: %q", msg)
	}
}

func TestTruncateDetail(t *testing.T) {
	if got := truncateDetail("short", 10); got != "short" {
		t.Errorf("短串不应截断: %q", got)
	}
	if got := truncateDetail("123456789012", 10); got != "1234567890..." {
		t.Errorf("长串截断错误: %q", got)
	}
	if got := truncateDetail("1234567890", 10); got != "1234567890" {
		t.Errorf("边界不应截断: %q", got)
	}
}

func TestConsecutiveFailMessage(t *testing.T) {
	msg := consecutiveFailMessage(4)
	if !strings.Contains(msg, "4") {
		t.Errorf("应含失败次数: %q", msg)
	}
}

func TestGoalAnchor(t *testing.T) {
	got := goalAnchor("out", "任务X", 5)
	if !strings.Contains(got, "out") || !strings.Contains(got, "任务X") || !strings.Contains(got, "第6轮") {
		t.Errorf("goalAnchor 输出不完整: %q", got)
	}
}
