package pack

import "testing"

func TestRussianAbbreviations(t *testing.T) {
	for _, pair := range [][2]string{{"ЖК Пример", "жилой комплекс Пример"}, {"ул. Тверская 10", "улица Тверская 10"}, {"просп. Мира", "проспект Мира"}} {
		if Normalize(pair[0]) != Normalize(pair[1]) {
			t.Errorf("%q != %q", Normalize(pair[0]), Normalize(pair[1]))
		}
	}
	if Normalize("ЖКХ 57к1") != "жкх 57к1" {
		t.Fatal("changed a word or house number")
	}
}
