package core

import (
	"reflect"
	"testing"
)

func TestNovelWords(t *testing.T) {
	src := []string{"Poznou zvedl jsem hlavu", "Z ničku je skýně vidět", "V jiných je třeba minutu několik"}
	for _, tc := range []struct {
		out  string
		want []string
	}{
		{"Poznou zvedl jsem hlavu.", nil},
		{"Pozorně zvedl jsem hlavu.", []string{"pozorne"}},
		{"Je třeba minut několik.", nil},  // ending fix
		{"Z ničku jeskyně vidět.", nil},   // word boundary joined
		{"Poznou zvedl 2 hlavy ok.", nil}, // digits and short words ignored, ending fix
		{"Z nitra je skryté vidět.", []string{"nitra", "skryte"}},
	} {
		if got := NovelWords(tc.out, src...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("NovelWords(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

// TestNovelWords_CzechRejections2026_10_12 replays every merge the production guard rejected
// in the Czech as-shipped measurement (prod-6bbbfef9, 190 clips × 2 runs, 21 rejections):
// the session's cloud raw and both phone transcripts as sent, and the word(s) the gateway
// log named. Reshaped heard words (an ending, a garbled repeat dropped, a compound split)
// must pass; words no recogniser heard — every one of them wrong against the reference —
// must stay flagged. Source: /Volumes/External/stt-eval/routes/cs/merge_gateway_logs.txt.
func TestNovelWords_CzechRejections2026_10_12(t *testing.T) {
	for _, tc := range []struct {
		clip, out string
		sources   []string
		heard     bool
	}{
		{"fleurs/cafe5/cs002", "červené", []string{"indikátor pomocí džusu z červeného země.", "Výdika je pomocí Jusus červeného zaví.", "Indikátor pomocí džusu z červeného zemí."}, true},
		{"fleurs/cafe5/cs005", "v tomto roce", []string{"No, v tomhle roku je to vůbec nějaký zelený, tak to vůbec není.", "Vamos dá škun", "No, but I won't disposition."}, false},
		{"fleurs/cafe5/cs009", "kamenovány", []string{"jiné moře salony možná mohly být kamenovalovány.", "Jynémosi Sahur možná mohli byť i kamedorov", "Jiné mostosauny, možná mohly být i kanalová."}, true},
		{"fleurs/clean/cs035", "magnetická", []string{"Působí mačetná dráha silou na galaxii strelce.", "Působy mať č nadráha selou na galaxi stralce", "Působí majčná dráha silou na galaxii strzelce."}, false},
		{"cv/cafe5/cv001", "trhaném", []string{"Na trhanostem zámku se vyjasníme.", "Nad Trhanovském záku se vyjasnilo", "Na trhanostém zámku se je jasně."}, true},
		{"cv/clean/cv004", "pozorně", []string{"Poznou zvedl jsem hlavu a naslouchal.", "Poznov zvedl jsem v hlavu a naslouchal", "Poznovu zvedl jsem hlavu a naslouchal."}, false},
		{"cv/cafe5/cv019", "nejdůležitější", []string{"Jaký je nejdílnější zákon nebo pravidlo?", "Já je nejdníší zákon nebo pravida", "Jaký je nejvýlnější zákon nebo pravidla?"}, false},
		{"cv/cafe5/cv020", "divadelníci", []string{"Tuto taktiku však používají divřidci.", "Taktikušek používají jeho zřídka.", "Tu taktiku však používají rozřídka."}, false},
		{"cv/clean/cv043", "vykřikla", []string{"Vykla na mě máma.", "Věkla na mě má", "Věkla na mě máma."}, false},
		{"cv/clean/cv057", "vyráběli", []string{"Tam, kde vyrvali matce s rotečka.", "Tam, kde vyrovaly mace srotečka", "Tam, kde vyrvala mace styrotečka."}, false},
		{"fleurs/cafe5/cs001", "místě", []string{"To je to na ní zbohaté.", "Potonaní zbo", "Total na místba."}, true}, // Parakeet heard "místba": a garble repaired by its stem
		{"fleurs/cafe5/cs002", "červené", []string{"indikátor pomocí džusu z červeného země.", "Výdika je pomocí Jusus červeného zaví.", "Indikátor pomocí džusu z červeného zemí."}, true},
		{"fleurs/clean/cs005", "koupání", []string{"Mnoho hotelů má k dispozici žihličku a žihličky pro koupnutí.", "Ménéhotelů má k dispozici jehličku a že hlíci prkno k zapojičení", "Mnoho hotelů má k dispozici žalíčku a žihlice prkno k zapojičení."}, true},
		{"fleurs/cafe5/cs009", "mosty, sauny", []string{"jiné moře salony možná mohly být kamenovalovány.", "Jynémosi Sahur možná mohli byť i kamedorov", "Jiné mostosauny, možná mohly být i kanalová."}, true},
		{"fleurs/clean/cs020", "řekne", []string{"Jestli vám druhý člověk leží.", "Jesli vám druhý človek", "Jestli vám druhý člověk ře."}, false},
		{"fleurs/clean/cs045", "je skryté", []string{"Z ničku je skýně vidět a slyšet jen velmi málo z tohoto světa.", "Znitřku je skyněne vidieť a syšet jen velmi málo z tohoto světa.", "Zničku je skyněj vidět a slyšet jen velmi málo z tohoto světa."}, false},
		{"cv/cafe5/cv001", "trhaném", []string{"Na trhanostem zámku se vyjasníme.", "Nad Trhanovském záku se vyjasnilo", "Na trhanostém zámku se je jasně."}, true},
		{"cv/clean/cv004", "pozorně", []string{"Poznou zvedl jsem hlavu a naslouchal.", "Poznov zvedl jsem v hlavu a naslouchal", "Poznovu zvedl jsem hlavu a naslouchal."}, false},
		{"cv/cafe5/cv007", "jsem", []string{"Nyní se připravím připravený, tak se nepohoří jako kdysi.", "V míni se preram připravení tak si nepohoří jako kdysi", "Nyní si připadám připravený, tak se nepohoří jako kdysi."}, false},
		{"cv/cafe5/cv028", "narkové", []string{"Byl jsem velmi mladý, ale nemyslím si, že jsem částí narkovou hru.", "", "Během pod doleka jin si našel čas i na rukovou hůbu."}, true},
		{"cv/clean/cv097", "nijak", []string{"Není některak rozdmychaný, ale spíše zadumaný.", "Není nikterák rozjuchaný, ale spíše zadumaný", "Není nikterak rozjouchaný, ale spíše zadumaný."}, false},
	} {
		got := NovelWords(tc.out, tc.sources...)
		if tc.heard && len(got) != 0 {
			t.Errorf("%s: %q flagged %v, want heard (reshaped heard word)", tc.clip, tc.out, got)
		}
		if !tc.heard && len(got) == 0 {
			t.Errorf("%s: %q passed, want flagged (no recogniser heard it)", tc.clip, tc.out)
		}
	}
}

func TestNovelWords_InventedWordsStayFlagged(t *testing.T) {
	src := []string{"Tam, kde vyrvali matce sirotečka.", "Jaký je nejdílnější zákon?", "Z níčku je skýně vidět"}
	for _, out := range []string{
		"Tam, kde vyráběli matce sirotečka.", // a different verb sharing 3 letters
		"Jaký je nejdůležitější zákon?",      // a common word near a garbled one
		"Z níčku je skryté vidět.",           // "je" + invented word joined ≈ "jeskyne"
		"Tam, kde prodávali matce sirotečka.",
	} {
		if got := NovelWords(out, src...); len(got) == 0 {
			t.Errorf("NovelWords(%q) passed, want an invented word flagged", out)
		}
	}
	// The reshaped forms of the same sources pass.
	for _, out := range []string{"Tam, kde vyrvala matce sirotečky.", "Z níčku je skýně vidět."} {
		if got := NovelWords(out, src...); len(got) != 0 {
			t.Errorf("NovelWords(%q) = %v, want none", out, got)
		}
	}
}
