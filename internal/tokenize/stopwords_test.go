package tokenize

import (
	"strings"
	"testing"
)

// inflectedRussian lists Russian function-word forms other than the lemma:
// oblique cases of pronouns and determiners, future forms of "быть", and
// closed-class adverbs and particles. Each one that leaks into the relevance
// set is a full overlap point for any fact that happens to contain it, so the
// set may only grow. Alphabetical, so a diff shows what was added.
const inflectedRussian = `
будем будет будете будешь будто буду будут быть
вами ваша ваше ваши ваших вдруг ведь вместо впрочем всегда всей всеми всю
давай давайте
зато зачем
именно ими иногда
какая какие каким каких какое какой какую кого ком кому кроме куда
либо лишь
меня мне мной мною мое моего моей мои моих мою моё
наконец нами наша наше нашего нашей наши наших нашу него нему нибудь
никогда ним ними нужно нём
однако около опять откуда
пожалуйста почему пусть
ради разве
сам сама сами само самого самой самом свое своего своей своему свои
своим своих свою своё сейчас совсем среди сюда
такая такие таким таких такое такой такую там твое твои твой твоя твоё
тебе тебя теми тех тобой тогда тому тою туда тут
хоть хотя
чего чему чуть чём
этим этими этих этом этому этою
`

func TestDefaultStopwords_CoversInflectedRussian(t *testing.T) {
	for _, w := range strings.Fields(inflectedRussian) {
		if _, ok := DefaultStopwords[w]; !ok {
			t.Errorf("%q must be a stopword", w)
		}
	}
}

// Relevance on a conversational Russian sentence keeps the content words and
// drops every inflected function word, whatever its case.
func TestRelevance_DropsInflectedRussianFunctionWords(t *testing.T) {
	got := Relevance("давай глянем, что не так с ним в этом конфиге у тех сервисов")
	kept := map[string]bool{}
	for _, tok := range got {
		kept[tok] = true
	}
	for _, fn := range []string{"давай", "ним", "этом", "тех"} {
		if kept[fn] {
			t.Errorf("function word %q leaked into %v", fn, got)
		}
	}
	for _, content := range []string{"глянем", "конфиге", "сервисов"} {
		if !kept[content] {
			t.Errorf("content word %q dropped from %v", content, got)
		}
	}
}
