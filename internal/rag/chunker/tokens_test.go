package chunker

import "testing"

func TestDetectLanguageChinese(t *testing.T) {
	text := "这是一个中文技术文档，主要介绍系统的安装、配置以及部署方式。"

	if got := DetectLanguage(text); got != LangChinese {
		t.Fatalf("中文识别错误: want=%q got=%q", LangChinese, got)
	}
}

func TestDetectLanguageEnglish(t *testing.T) {
	text := "This document explains how to install and configure the application."

	if got := DetectLanguage(text); got != LangEnglish {
		t.Fatalf("英文识别错误: want=%q got=%q", LangEnglish, got)
	}
}

func TestDetectLanguageGerman(t *testing.T) {
	text := "Das ist ein Dokument und die Konfiguration ist auf dem Server verfügbar."

	if got := DetectLanguage(text); got != LangGerman {
		t.Fatalf("德文识别错误: want=%q got=%q", LangGerman, got)
	}
}

func TestDetectLanguageGermanUmlaut(t *testing.T) {
	text := "Über diese Funktion können Benutzer zusätzliche Einstellungen ändern."

	if got := DetectLanguage(text); got != LangGerman {
		t.Fatalf("德语 Umlaut 识别错误: want=%q got=%q", LangGerman, got)
	}
}

func TestDetectLanguageMixed(t *testing.T) {
	text := "这是一个 RAG system，它使用 vector search 和 BM25 进行混合检索。"

	if got := DetectLanguage(text); got != LangMixed {
		t.Fatalf("混合语言识别错误: want=%q got=%q", LangMixed, got)
	}
}

func TestDetectLanguageWithoutLetters(t *testing.T) {
	if got := DetectLanguage("123456 !!!"); got != LangMixed {
		t.Fatalf("没有有效语言字符时应返回 mixed: got=%q", got)
	}
}

func TestApproxTokenCount(t *testing.T) {
	if got := ApproxTokenCountFromRuneLen(400, LangEnglish); got != 100 {
		t.Fatalf("英文 Token 估算错误: want=100 got=%d", got)
	}

	if got := ApproxTokenCountFromRuneLen(170, LangChinese); got != 100 {
		t.Fatalf("中文 Token 估算错误: want=100 got=%d", got)
	}

	if got := ApproxTokenCount("", LangEnglish); got != 0 {
		t.Fatalf("空文本 Token 应为0: got=%d", got)
	}
}

func TestUnknownLanguageFallsBackToMixed(t *testing.T) {
	got := ApproxTokenCountFromRuneLen(300, "unknown")
	want := 100 // mixed = 3 chars/token

	if got != want {
		t.Fatalf("未知语言应该回退 mixed: want=%d got=%d", want, got)
	}
}

func TestCharsForTokenLimit(t *testing.T) {
	tests := []struct {
		name   string
		tokens int
		lang   string
		want   int
	}{
		{"English", 100, LangEnglish, 360},
		{"German", 100, LangGerman, 405},
		{"Chinese", 100, LangChinese, 153},
		{"Mixed", 100, LangMixed, 270},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CharsForTokenLimit(tt.tokens, tt.lang)

			if got != tt.want {
				t.Fatalf("字符预算错误: want=%d got=%d", tt.want, got)
			}
		})
	}
}
