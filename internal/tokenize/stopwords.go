package tokenize

import "strings"

// DefaultStopwords is the built-in English + Russian function-word set used by
// Relevance, i.e. on both sides of every overlap count (fact keywords and the
// query). A function word left in the set is worth a full overlap point (3.0,
// more than the whole recency term), so a long body full of them out-ranked a
// genuinely relevant fact — the "body lottery" the internal ranker audit
// found.
//
// Only closed-class words belong here: articles, pronouns, determiners,
// conjunctions, prepositions, auxiliaries and particles. Content words stay
// relevance signal however common they are. Words shorter than MinTokenRunes
// are dropped by Tokenize before this set is consulted, so listing them is
// harmless but unnecessary.
var DefaultStopwords = stopwordSet(englishFunctionWords, russianFunctionWords)

// englishFunctionWords, one group per line.
var englishFunctionWords = []string{
	"the all any each both some such most more much many other same one two new old",                      // determiners, quantifiers
	"you her his him its our your they them their this that these those who what which",                   // pronouns
	"and but for than then when where while because how",                                                  // conjunctions
	"with from into onto over under about above after before between during",                              // prepositions
	"are was were been being has had have did does done can may will would could should shall must might", // auxiliaries
	"not now here there only also just very too again out",                                                // adverbs
	"see use used using get got make made let",                                                            // light verbs
}

// russianFunctionWords: Russian inflects its pronouns and determiners, so a
// lemma alone leaves its oblique cases in the relevance set ("этом", "тому",
// "своих" each scored like a content word). Declinable words are listed as
// full paradigms, one lemma per line, both "ё" and "е" spellings; the
// indeclinable rest follows by part of speech.
var russianFunctionWords = []string{
	// Personal and reflexive pronouns.
	"я меня мне мной мною",
	"ты тебя тебе тобой тобою",
	"он его него ему нему им ним нём",
	"она её ее неё нее ей ней ею нею",
	"оно",
	"мы нас нам нами",
	"вы вас вам вами",
	"они их них ими ними",
	"себя себе собой собою",
	// Interrogative and relative pronouns.
	"кто кого кому кем ком",
	"что чего чему чем чём",
	"который которая которое которые которого которой которому которым котором которых которыми которую",
	"какой какая какое какие какого какому каким каком каких какими какую",
	// Demonstratives and determiners.
	"этот эта это эти этого этой этому этим этом этих этими эту этою",
	"тот та то те того той тому тем том тех теми ту тою",
	"такой такая такое такие такого такому таким таком таких такими такую",
	"весь вся всё все всего всей всему всем всём всех всеми всю",
	"сам сама само сами самого самой самому самим самом самих самими саму",
	// Possessives.
	"мой моя моё мое мои моего моей моему моим моём моих моими мою",
	"твой твоя твоё твое твои твоего твоей твоему твоим твоём твоих твоими твою",
	"свой своя своё свое свои своего своей своему своим своём своих своими свою",
	"наш наша наше наши нашего нашей нашему нашим нашем наших нашими нашу",
	"ваш ваша ваше ваши вашего вашей вашему вашим вашем ваших вашими вашу",
	// "быть": past, present and future forms.
	"быть был была было были есть буду будешь будет будем будете будут",
	// Conjunctions.
	"если или когда чтобы чтоб потому поэтому также тоже хотя хоть зато однако либо нибудь будто",
	// Prepositions.
	"для без над под при про после перед через между кроме ради среди вместо около",
	// Adverbs of place, time and degree.
	"где куда откуда здесь тут там туда сюда",
	"уже теперь сейчас потом затем тогда иногда всегда никогда опять наконец",
	"как так очень почти более менее только даже именно совсем чуть лишь",
	"зачем почему",
	// Particles, modal words and conversational fillers.
	"да нет вот ещё еще ведь вдруг разве впрочем надо нужно может можно",
	"давай давайте пусть пожалуйста",
}

// stopwordSet flattens whitespace-separated word groups into a lookup set.
func stopwordSet(groups ...[]string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, lines := range groups {
		for _, line := range lines {
			for _, w := range strings.Fields(line) {
				set[w] = struct{}{}
			}
		}
	}
	return set
}

// Relevance tokenizes text for ranking use: lowercased, Unicode-aware, minimum
// rune length, with DefaultStopwords removed. This is the single policy point
// for "what tokens count as relevance signal" — keyword population and query
// building both go through it so a stopword never appears on either side.
func Relevance(text string) []string {
	return Tokenize(text, DefaultStopwords)
}
