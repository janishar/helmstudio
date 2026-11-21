//! Swedish Roman numeral classifier (keyed cardinal and keyed regnal/papal ordinals).

use super::number::{cardinal, ordinal, roman_to_int};

const KEY_WORDS: &[(&str, &str)] = &[
    ("kapitel", "kapitel"),
    ("Kapitel", "Kapitel"),
    ("KAPITEL", "Kapitel"),
    ("kap.", "kapitel"),
    ("Kap.", "Kapitel"),
    ("KAP.", "Kapitel"),
    ("del", "del"),
    ("Del", "Del"),
    ("DEL", "DEL"),
    ("avsnitt", "avsnitt"),
    ("Avsnitt", "Avsnitt"),
    ("AVSNITT", "Avsnitt"),
    ("avdelning", "avdelning"),
    ("Avdelning", "Avdelning"),
    ("AVDELNING", "Avdelning"),
    ("artikel", "artikel"),
    ("Artikel", "Artikel"),
    ("ARTIKEL", "Artikel"),
    ("paragraf", "paragraf"),
    ("Paragraf", "Paragraf"),
    ("PARAGRAF", "Paragraf"),
    ("band", "band"),
    ("Band", "Band"),
    ("BAND", "Band"),
    ("volym", "volym"),
    ("Volym", "Volym"),
    ("VOLYM", "Volym"),
    ("bok", "bok"),
    ("Bok", "Bok"),
    ("BOK", "BOK"),
    ("akt", "akt"),
    ("Akt", "Akt"),
    ("AKT", "AKT"),
    ("scen", "scen"),
    ("Scen", "Scen"),
    ("SCEN", "Scen"),
    ("sekel", "sekel"),
    ("Sekel", "Sekel"),
    ("SEKEL", "Sekel"),
    ("århundrade", "århundrade"),
    ("Århundrade", "Århundrade"),
    ("ÅRHUNDRADE", "Århundrade"),
    ("bilaga", "bilaga"),
    ("Bilaga", "Bilaga"),
    ("BILAGA", "Bilaga"),
    ("tabell", "tabell"),
    ("Tabell", "Tabell"),
    ("TABELL", "Tabell"),
    ("figur", "figur"),
    ("Figur", "Figur"),
    ("FIGUR", "Figur"),
    ("klass", "klass"),
    ("Klass", "Klass"),
    ("KLASS", "Klass"),
    ("typ", "typ"),
    ("Typ", "Typ"),
    ("TYP", "TYP"),
    ("fas", "fas"),
    ("Fas", "Fas"),
    ("FAS", "FAS"),
    ("steg", "steg"),
    ("Steg", "Steg"),
    ("STEG", "Steg"),
    ("nivå", "nivå"),
    ("Nivå", "Nivå"),
    ("NIVÅ", "Nivå"),
    ("grupp", "grupp"),
    ("Grupp", "Grupp"),
    ("GRUPP", "Grupp"),
];

const MONARCH_NAMES: &[&str] = &[
    "Karl",
    "Carl",
    "Gustav",
    "Gustaf",
    "Oscar",
    "Oskar",
    "Erik",
    "Eric",
    "Johan",
    "Sigismund",
    "Kristina",
    "Christina",
    "Ulrika",
    "Fredrik",
    "Adolf",
    "Magnus",
    "Birger",
    "Valdemar",
    "Albrekt",
    "Kristian",
    "Christian",
    "Margareta",
    "Margrethe",
    "Harald",
    "Håkon",
    "Haakon",
    "Olav",
    "Olof",
    "Sven",
    "Knut",
    "Ludvig",
    "Louis",
    "Henrik",
    "Henry",
    "Edvard",
    "Edward",
    "Georg",
    "George",
    "Charles",
    "Jakob",
    "James",
    "Vilhelm",
    "Wilhelm",
    "William",
    "Filip",
    "Philip",
    "Felipe",
    "Ferdinand",
    "Alfons",
    "Alfonso",
    "Peter",
    "Pjotr",
    "Nikolaj",
    "Alexander",
    "Katarina",
    "Elisabet",
    "Elizabeth",
    "Victoria",
    "Viktoria",
    "Maria",
    "Otto",
    "Konrad",
    "Frans",
    "Franciskus",
    "Johannes",
    "Paulus",
    "Benedikt",
    "Benedictus",
    "Pius",
    "Leo",
    "Gregorius",
    "Clemens",
    "Innocentius",
    "Urban",
    "Sixtus",
    "Bonifatius",
    "Ramses",
    "Ptolemaios",
    "Napoleon",
];

type KeyIndex = std::collections::HashMap<char, Vec<(&'static str, &'static str)>>;
static KEY_INDEX: std::sync::LazyLock<KeyIndex> = std::sync::LazyLock::new(|| {
    let mut index = KeyIndex::new();
    for &(key, spoken) in KEY_WORDS {
        if let Some(initial) = key.chars().next() {
            index.entry(initial).or_default().push((key, spoken));
        }
    }
    index
});

static NAME_INDEX: std::sync::LazyLock<std::collections::HashMap<char, Vec<&'static str>>> =
    std::sync::LazyLock::new(|| {
        let mut index: std::collections::HashMap<char, Vec<&'static str>> = std::collections::HashMap::new();
        for &name in MONARCH_NAMES {
            if let Some(initial) = name.chars().next() {
                index.entry(initial).or_default().push(name);
            }
        }
        index
    });

/// Match every keyed Roman numeral prefix, returning spoken text and absolute end.
pub fn match_roman(text: &[char], start: usize) -> Vec<(String, usize)> {
    let rem = &text[start..];
    let mut out = Vec::new();
    let Some(&initial) = rem.first() else { return out };
    for &(key, spoken_key) in KEY_INDEX.get(&initial).into_iter().flatten() {
        let key_len = key.chars().count();
        if !rem.iter().copied().take(key_len).eq(key.chars()) || rem.get(key_len) != Some(&' ') {
            continue;
        }
        let at = key_len + 1;
        let mut roman = String::new();
        for (i, &c) in rem[at..].iter().enumerate() {
            if !matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
                break;
            }
            roman.push(c);
            if matches!(roman.as_str(), "C" | "D" | "L" | "M") {
                continue;
            }
            if let Some(value) = roman_to_int(&roman) {
                if let Some(number) = cardinal(&value.to_string(), false, false) {
                    out.push((format!("{spoken_key}\u{a0}{}", number.replace(' ', "\u{a0}")), start + at + i + 1));
                }
            }
        }
    }
    for &name in NAME_INDEX.get(&initial).into_iter().flatten() {
        let name_len = name.chars().count();
        if rem.get(name_len) != Some(&' ') {
            continue;
        }
        let input = &rem[..name_len];
        if !input.iter().copied().eq(name.chars())
            && !input.iter().copied().eq(name.chars().flat_map(char::to_uppercase))
        {
            continue;
        }
        let name_out: String = input.iter().collect();
        let at = name_len + 1;
        let mut roman = String::new();
        for (i, &c) in rem[at..].iter().enumerate() {
            if !matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
                break;
            }
            roman.push(c);
            let Some(value) = roman_to_int(&roman) else { continue };
            if value > 39 {
                continue;
            }
            let Some(number) = ordinal(&value.to_string()) else { continue };
            let end = at + i + 1;
            let spoken = format!("{name_out}\u{a0}den\u{a0}{}", number.replace(' ', "\u{a0}"));
            out.push((spoken.clone(), start + end));
            if rem.get(end) == Some(&'.') {
                out.push((spoken.clone(), start + end + 1));
            }
            if rem.get(end) == Some(&':') && matches!(rem.get(end + 1), Some('s' | 'S')) {
                out.push((format!("{spoken}s"), start + end + 2));
            }
        }
    }
    out
}
