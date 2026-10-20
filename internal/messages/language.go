package messages

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/luizfreitas27/ajai/internal/textmatch"
)

// English function words, matched after textmatch.Fold.
var english = []string{"the", "and", "you", "we", "our", "are", "will", "with", "your", "this", "that", "is", "to", "of", "from", "their", "have", "be"}

// Function words that Portuguese and Spanish share. They count toward the
// Romance side, not toward either language.
var shared = []string{"de", "que", "para", "por", "e", "y", "o", "a", "la", "no", "em", "en", "um", "una", "uma", "os", "as", "los", "las", "com", "con"}

// Words only one Romance language uses in a job posting. Each is distinct from
// the other language's words after textmatch.Fold, and none is in shared.
var exclusive = map[string][]string{
	"pt": {"da", "das", "dos", "ao", "nao", "voce", "sao", "seu", "sua", "tambem", "pelo", "pela", "vaga", "nossa", "nosso", "trabalho", "oferecemos"},
	"es": {"del", "el", "usted", "nosotros", "nuestro", "nuestra", "pero", "es", "son", "vacante", "puede", "puedes", "trabajo", "puesto", "buscamos", "ofrecemos", "tambien"},
}

// domain matches a web address or an e-mail address in folded text. Its words
// are not counted: "acme.com" has a "com" that is no Romance word.
var domain = regexp.MustCompile(`\S*@\S*|(?:https?://)?\S*\.[a-z]{2,6}\b(?:/\S*)?`)

// DetectLanguage returns "en", "pt" or "es" for the language of a job posting's
// text, or "" when it is unsure. Postings often have English sections in
// Portuguese or Spanish, so English must clearly win: it needs at least four
// function words for each Romance one. Otherwise the Romance side decides
// between Portuguese and Spanish by their exclusive words. Text with about as
// many English and Romance words is mixed, so it is unsure. Text with fewer
// than minHits function words is unsure too.
func DetectLanguage(text string) string {
	const minHits = 5
	words := strings.FieldsFunc(domain.ReplaceAllString(textmatch.Fold(text), " "), func(r rune) bool { return !unicode.IsLetter(r) })
	en, rom := 0, 0
	pt, es := 0, 0
	for _, w := range words {
		switch {
		case slices.Contains(english, w):
			en++
		case slices.Contains(shared, w):
			rom++
		case slices.Contains(exclusive["pt"], w):
			rom++
			pt++
		case slices.Contains(exclusive["es"], w):
			rom++
			es++
		}
	}
	switch {
	case en+rom < minHits:
		return ""
	case en >= 4*rom:
		return "en"
	case en >= minHits && rom >= minHits && en <= 2*rom && rom <= 2*en:
		return ""
	case pt > es:
		return "pt"
	case es > pt:
		return "es"
	}
	return ""
}
