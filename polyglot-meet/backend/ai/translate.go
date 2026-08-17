package ai

import (
	"fmt"
	"strings"
)

const glossary = `Clario, WebRTC, Zoom, Teams, caption, captions, mic, unmute`

func interpreterSystem(sourceLang, targetLang string) string {
	src := LangName(sourceLang)
	tgt := LangName(targetLang)
	return fmt.Sprintf(`Translate this spoken %s into natural spoken %s.
Output ONLY the translation. No labels, notes, or extra sentences.
Do not invent words. Keep names, numbers, and brands unchanged (%s).
%s`, src, tgt, glossary, pairHint(sourceLang, targetLang))
}

func sanitizeTranscript(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	lower := strings.ToLower(t)
	leaks := []string{
		"ignore background noise",
		"ignore background voice",
		"ignore background",
		"transcribe only clear speech",
		"transcribe only",
		"live conversation spoken",
		"clear speech",
		"background noise",
	}
	for _, leak := range leaks {
		if strings.Contains(lower, leak) {
			return ""
		}
	}
	return t
}

func pairHint(src, tgt string) string {
	switch tgt {
	case "hi":
		return "- Use natural spoken Hindi in Devanagari. Everyday words, not stiff formal Hindi."
	case "es":
		return "- Use natural conversational Spanish. Keep it meeting-friendly, not slang-heavy."
	case "de":
		return "- Use natural spoken German. Keep names exactly as given."
	case "ar":
		return "- Use clear Modern Standard Arabic suitable for captions and speech."
	case "ja":
		return "- Use polite spoken Japanese (です/ます) suitable for a work call."
	case "ko":
		return "- Use polite spoken Korean suitable for a work call."
	case "zh":
		return "- Use spoken Simplified Mandarin, not literary Chinese."
	case "fr":
		return "- Use natural spoken French, not overly literary."
	case "pt":
		return "- Use natural Brazilian Portuguese unless the source is clearly European."
	default:
		if src == "hi" && tgt == "en" {
			return "- Output natural spoken English. Keep Indian names unchanged."
		}
		return ""
	}
}

func shot(lang, kind string) string {
	table := map[string]map[string]string{
		"en": {"hear": `"Can you hear me?"`, "name": `"I am Noman Ansari."`, "wait": `"Give me one second."`},
		"hi": {"hear": `"क्या आप मुझे सुन सकते हैं?"`, "name": `"मैं नोमान अंसारी हूँ।"`, "wait": `"एक सेकंड दीजिए।"`},
		"es": {"hear": `"¿Me escuchas?"`, "name": `"Soy Noman Ansari."`, "wait": `"Dame un segundo."`},
		"de": {"hear": `"Kannst du mich hören?"`, "name": `"Ich bin Noman Ansari."`, "wait": `"Einen Moment bitte."`},
		"fr": {"hear": `"Vous m'entendez ?"`, "name": `"Je suis Noman Ansari."`, "wait": `"Donnez-moi une seconde."`},
		"zh": {"hear": `"你能听到我吗？"`, "name": `"我是 Noman Ansari。"`, "wait": `"等我一秒。"`},
		"ar": {"hear": `"هل تسمعني؟"`, "name": `"أنا نومان أنصاري."`, "wait": `"أعطني ثانية واحدة."`},
		"pt": {"hear": `"Você me escuta?"`, "name": `"Eu sou Noman Ansari."`, "wait": `"Me dá um segundo."`},
		"ja": {"hear": `"聞こえますか？"`, "name": `"Noman Ansariです。"`, "wait": `"少し待ってください。"`},
		"ko": {"hear": `"제 말 들리세요?"`, "name": `"저는 Noman Ansari입니다."`, "wait": `"잠시만요."`},
	}
	if m, ok := table[lang]; ok {
		if s, ok := m[kind]; ok {
			return s
		}
	}
	return table["en"][kind]
}

func cleanTranslation(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")
	lower := strings.ToLower(s)
	prefixes := []string{
		"translation:",
		"translated:",
		"here is the translation:",
		"here's the translation:",
		"output:",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			s = strings.Trim(s, "\"'`")
			break
		}
	}
	return strings.TrimSpace(s)
}
