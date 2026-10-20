package messages

import (
	"strings"
	"unicode"

	"github.com/luizfreitas27/ajai/internal/textmatch"
)

// Function words that only one language uses in a job posting. A word shared by
// two languages (de, que, para, sobre) is left out, so it cannot tip the count.
// Words are matched after textmatch.Fold, so accents do not matter.
var stopwords = map[string][]string{
	"en": {"the", "and", "you", "we", "our", "are", "will", "with", "your", "this", "that", "is", "to", "of", "from", "their", "have", "be"},
	"pt": {"do", "da", "dos", "das", "com", "uma", "um", "nao", "voce", "nos", "sao", "seu", "sua", "tambem", "pelo", "pela", "ao", "em", "vaga"},
	"es": {"del", "las", "los", "el", "una", "usted", "nosotros", "con", "pero", "es", "son", "vacante", "estamos", "puede", "puedes"},
}

// DetectLanguage returns "en", "pt" or "es" for the language of a job posting's
// text, or "" when it is unsure. Text is unsure when it has fewer than
// minHits function words of the leading language, or that language has less
// than twice the hits of the runner-up. Titles and short lines are unsure.
func DetectLanguage(text string) string {
	const minHits = 5
	words := strings.FieldsFunc(textmatch.Fold(text), func(r rune) bool { return !unicode.IsLetter(r) })
	hits := map[string]int{}
	for _, w := range words {
		for lang, list := range stopwords {
			for _, s := range list {
				if w == s {
					hits[lang]++
				}
			}
		}
	}
	best, second, bestLang := 0, 0, ""
	for _, lang := range []string{"en", "pt", "es"} {
		switch n := hits[lang]; {
		case n > best:
			second, best, bestLang = best, n, lang
		case n > second:
			second = n
		}
	}
	if best < minHits || best < 2*second {
		return ""
	}
	return bestLang
}
