package messages

import "testing"

const (
	brasil        = "Olá {first_name}, acabei de me candidatar à vaga de {title} na {company}. Fico à disposição para conversar e no aguardo de um contato. Obrigado!"
	internacional = "Hi {first_name}, I just applied for the {title} role at {company}. I'd be glad to talk whenever it suits you, and I look forward to hearing from you. Thanks!"
)

func TestRenderFillsThePlaceholdersAndTidiesAMissingName(t *testing.T) {
	job := Job{Title: "Dev Python", Company: "Acme"}
	for _, tc := range []struct {
		name, template string
		r              Recipient
		want           string
	}{
		{"brasil with a first name", brasil, Recipient{Name: "Ana Maria Silva"},
			"Olá Ana, acabei de me candidatar à vaga de Dev Python na Acme. Fico à disposição para conversar e no aguardo de um contato. Obrigado!"},
		{"brasil without a name", brasil, Recipient{Name: "  "},
			"Olá, acabei de me candidatar à vaga de Dev Python na Acme. Fico à disposição para conversar e no aguardo de um contato. Obrigado!"},
		{"abroad with a first name", internacional, Recipient{Name: "John Doe"},
			"Hi John, I just applied for the Dev Python role at Acme. I'd be glad to talk whenever it suits you, and I look forward to hearing from you. Thanks!"},
		{"abroad without a name", internacional, Recipient{},
			"Hi, I just applied for the Dev Python role at Acme. I'd be glad to talk whenever it suits you, and I look forward to hearing from you. Thanks!"},
		{"spanish without a name", "Hola {first_name}, acabo de postularme a {title} en {company}.", Recipient{},
			"Hola, acabo de postularme a Dev Python en Acme."},
		{"spanish with a first name", "Hola {first_name}, acabo de postularme a {title} en {company}.", Recipient{Name: "Ana Silva"},
			"Hola Ana, acabo de postularme a Dev Python en Acme."},
		{"a name at the start of the text", "{first_name}, hello {title}", Recipient{},
			"hello Dev Python"},
		{"surrounding blanks trimmed", "  Hi {title}  ", Recipient{}, "Hi Dev Python"},
		{"a blank template renders nothing", "   ", Recipient{Name: "Ana"}, ""},
		{"a template's own leading comma is kept without a name", ", hello {title}", Recipient{}, ", hello Dev Python"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Render(tc.template, tc.r, job); got != tc.want {
				t.Errorf("Render() = %q\nwant       %q", got, tc.want)
			}
		})
	}
}
