package questions

import (
	"testing"

	"github.com/luizfreitas27/ajai/internal/config"
)

// skilled is pinned with skill years, as the user would list them.
func skilled() config.Config {
	c := pinned()
	c.Questions.SkillYears = []string{"java: 3", ".net: 5", "c#: 5", "python: 4", "aws: 3", "cloud: 3",
		"computação em nuvem: 3", "angular: 2", "kafka: 2", "dev fullstack: 4"}
	return c
}

func TestSkillYearsAnswerHowLongWithAListedTechnology(t *testing.T) {
	a, _ := answerer(t, skilled())
	for label, want := range map[string]string{ // the questions of the 2026-10-04 dry run, and English ones
		"Há quantos anos você já usa Computação em nuvem no trabalho?":                       "3",
		"Há quantos anos você já usa COMPUTACAO EM NUVEM no trabalho?":                       "3", // case and accents aside
		"Quanto tempo de experiência profissional possui com .Net?":                          "5",
		"Possui quanto tempo de experiência com C#?":                                         "5",
		"Há quanto tempo você atua como Dev Fullstack?":                                      "4",
		"Desenvolvimento .Net AWS: quanto tempo de experiência profissional possui com AWS?": "3",
		"How many years of work experience do you have with Python?":                         "4",
		"How long have you worked with AWS? (EKS, SQS, SNS, S3)":                             "3",
		"Quanto tempo de experiência você tem com Java e Python?":                            "3", // the smallest of those named
	} {
		if d := a.Answer(text(label), job); d.Action != Fill || d.Value != want || d.Source != "config" {
			t.Errorf("%q: %+v, want %s from the config", label, d, want)
		}
	}
}

func TestSkillYearsLeaveOtherTextQuestionsAsTheyWere(t *testing.T) {
	a, _ := answerer(t, skilled())
	for _, label := range []string{
		"Há quantos anos você já usa Mendix no trabalho?",      // not listed
		"How many years of JavaScript experience do you have?", // java is not JavaScript
		"Quantos anos você tem?",                               // age
		"Qual foi o seu maior projeto em Java?",                // not how long
	} {
		if d := a.Answer(text(label), job); d.Action != Blocked || !d.Remember {
			t.Errorf("%q: %+v, want it left for the review queue", label, d)
		}
	}
	if d := a.Answer(text("How many years of professional experience do you have?"), job); d.Value != "6" {
		t.Errorf("the total still answers a question about the total: %+v", d)
	}
	if d := a.Answer(text("What is your expected annual salary for this Java role, in years of pay?"), job); d.Value == "3" {
		t.Errorf("a sensitive question is never answered from skill years: %+v", d)
	}
}

func TestSkillYearsAnswerYesToExperienceQuestions(t *testing.T) {
	a, _ := answerer(t, skilled())
	for _, tc := range []struct {
		q    Question
		want string
	}{
		{dropdown("Possui experiência com Angular?", "Selecionar opção", "Yes", "No"), "Yes"},
		{dropdown("Tem Experiência com Cloud (AWS, Azure ou GCP)?", "Selecionar opção", "Sim", "Não"), "Sim"},
		{radio("Tem experiência comprovada com Java/C#/Python?", "Sim", "Não"), "Sim"},
		{dropdown("Possui experiência com mensagerias como Kafka, RabbitMQ?", "Selecionar opção", "Yes", "No"), "Yes"},
		{dropdown("Você possui domínio de Java e Spring Boot e experiência com APIs RESTful?", "Selecionar opção", "Yes", "No"), "Yes"},
		{radio("Do you have hands-on experience with AWS?", "Yes", "No"), "Yes"},
	} {
		if d := a.Answer(tc.q, job); d.Action != Choose || d.Value != tc.want || d.Source != "config" {
			t.Errorf("%q: %+v, want %q from the config", tc.q.Label, d, tc.want)
		}
	}
}

func TestSkillYearsNeverAnswerNoNorOtherYesNoQuestions(t *testing.T) {
	a, _ := answerer(t, skilled())
	for _, q := range []Question{
		dropdown("Possui experiência com arquitetura hexagonal?", "Selecionar opção", "Yes", "No"), // not listed: never "No"
		dropdown("Possui experiência com JavaScript?", "Selecionar opção", "Yes", "No"),            // java is not JavaScript
		dropdown("Aceita trabalhar com Java como PJ?", "Selecionar opção", "Sim", "Não"),           // not about experience
		dropdown("Qual seu nível de experiência com Java?", "Selecionar opção", "Básico", "Intermediário", "Avançado"),
	} {
		if d := a.Answer(q, job); d.Action != Blocked || !d.Remember {
			t.Errorf("%q: %+v, want it left for the review queue", q.Label, d)
		}
	}
	sensitive := radio("Do you have experience with Java projects that need a security clearance?", "Yes", "No")
	if d := a.Answer(sensitive, job); d.Action == Choose && d.Value == "Yes" {
		t.Errorf("a sensitive question is never answered from skill years: %+v", d)
	}
}
