package messages

import "testing"

func TestDetectLanguageReadsARealPostingInItsLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"english posting", "We are looking for a Senior Python Developer to join our team. You will design and build APIs, " +
			"work with the data platform, and collaborate with product managers. Experience with Django and PostgreSQL is a plus. " +
			"We offer remote work and a flexible schedule.", "en"},
		{"portuguese posting", "Procuramos um Desenvolvedor Python Sênior para integrar nossa equipe. Você irá projetar e desenvolver " +
			"APIs, trabalhar com a plataforma de dados e colaborar com os times de produto. Experiência com Django e PostgreSQL é um " +
			"diferencial. Oferecemos trabalho remoto e horário flexível para você.", "pt"},
		{"spanish posting", "Buscamos un Desarrollador Python Senior para unirse a nuestro equipo. Usted diseñará y construirá APIs, " +
			"trabajará con la plataforma de datos y colaborará con los equipos de producto. La experiencia con Django y PostgreSQL " +
			"es un plus. Ofrecemos trabajo remoto y horario flexible para los que se postulen.", "es"},
		{"brazilian posting with English sections", "Vaga para Desenvolvedor Go. Requirements: 5+ years of experience with Go and " +
			"the AWS stack. You will work with our team and the product. Benefits: VR, VA, plano de saúde.", "pt"},
		{"english posting with one Portuguese benefit", "We are looking for a Senior Python Developer to join our team. You will " +
			"design and build APIs, work with the data platform, and collaborate with product managers. Experience with Django and " +
			"PostgreSQL is a plus. We offer remote work and a flexible schedule. Benefits: health insurance, plano de saúde.", "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectLanguage(tc.text); got != tc.want {
				t.Errorf("DetectLanguage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectLanguageIsUnsureForShortMixedOrBlankText(t *testing.T) {
	for _, tc := range []struct {
		name, text string
	}{
		{"blank", ""},
		{"a title only", "Senior Python Developer"},
		{"a short English line", "Hi, we are hiring"},
		{"half English and half Portuguese",
			"We are hiring and you will work with us. " +
				"Você irá trabalhar com a equipe em um time do produto, com os dados da empresa."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectLanguage(tc.text); got != "" {
				t.Errorf("DetectLanguage() = %q, want \"\" (unsure)", got)
			}
		})
	}
}

func TestDetectLanguageDoesNotCountADomainName(t *testing.T) {
	// Without the fix, the two "com" of acme.com are Romance words and tip the count.
	const text = "We are hiring and you will work with us. Apply at acme.com or acme.com."
	if got := DetectLanguage(text); got != "en" {
		t.Errorf("DetectLanguage() = %q, want \"en\"", got)
	}
}

func TestExclusiveWordsStayDistinctAfterFolding(t *testing.T) {
	// A word shared by two languages after accents are dropped (também, também
	// folded to tambem and tambien) would count for both and decide nothing.
	seen := map[string]string{}
	for lang, list := range exclusive {
		for _, w := range list {
			if other, dup := seen[w]; dup {
				t.Errorf("%q is exclusive to both %s and %s", w, other, lang)
			}
			seen[w] = lang
		}
	}
	for _, w := range shared {
		if lang, dup := seen[w]; dup {
			t.Errorf("%q is shared and also exclusive to %s", w, lang)
		}
	}
}
