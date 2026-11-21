use super::{
    datetime,
    graph_utils::{cardinal, glue_suffix, number, ordinal},
    numeric::{range_weight, split_suffix},
};
use std::collections::HashSet;
use std::sync::LazyLock;
fn name(s: &str) -> String {
    format!("name: \"{}\"", s.replace(' ', "\u{a0}"))
}
fn roman_value(s: &str) -> Option<usize> {
    let symbols = [(100, "C"), (90, "XC"), (50, "L"), (40, "XL"), (10, "X"), (9, "IX"), (5, "V"), (4, "IV"), (1, "I")];
    let mut rest = s;
    let mut n = 0;
    for (v, sym) in symbols {
        while let Some(next) = rest.strip_prefix(sym) {
            n += v;
            rest = next;
        }
    }
    if !rest.is_empty() || n == 0 || n > 100 {
        return None;
    }
    let mut remaining = n;
    let mut canonical = String::new();
    for (v, sym) in symbols {
        while remaining >= v {
            remaining -= v;
            canonical.push_str(sym);
        }
    }
    if canonical == s { Some(n) } else { None }
}
const PRE: &[&str] = &[
    "თავი",
    "თავის",
    "თავში",
    "ნაწილი",
    "ნაწილის",
    "ნაწილში",
    "ტომი",
    "ტომის",
    "ტომში",
    "წიგნი",
    "წიგნის",
    "წიგნში",
    "მუხლი",
    "მუხლის",
    "მუხლში",
    "კლასი",
    "კლასის",
    "კლასში",
    "აქტი",
    "აქტის",
    "აქტში",
    "სურათი",
    "სურათის",
    "პარაგრაფი",
    "კარი",
    "ეტაპი",
    "დონე",
    "Chapter",
    "chapter",
    "Section",
    "section",
    "Book",
    "book",
    "Volume",
    "volume",
    "Part",
    "part",
    "Act",
    "act",
    "Scene",
    "scene",
    "Class",
    "class",
];
const POST: &[&str] = &[
    "საუკუნე",
    "საუკუნის",
    "საუკუნეს",
    "საუკუნეში",
    "საუკუნით",
    "საუკუნეები",
    "საუკუნეების",
    "საუკუნეებს",
    "საუკუნეებში",
    "ათასწლეული",
    "ათასწლეულის",
    "ათასწლეულში",
    "კლასი",
    "კლასის",
    "კლასში",
    "ტომი",
    "ტომის",
    "ნაწილი",
    "ნაწილის",
    "თავი",
    "თავის",
];
const MONARCH: &[&str] = &[
    "დავით",
    "დავითის",
    "გიორგი",
    "გიორგის",
    "ერეკლე",
    "ერეკლეს",
    "ვახტანგ",
    "ვახტანგის",
    "თამარ",
    "თამარის",
    "ბაგრატ",
    "ბაგრატის",
    "დემეტრე",
    "დემეტრეს",
    "ლაშა",
    "რუსუდან",
    "ალექსანდრე",
    "ალექსანდრეს",
    "სოლომონ",
    "სოლომონის",
    "როსტომ",
    "როსტომის",
    "თეიმურაზ",
    "თეიმურაზის",
    "არჩილ",
    "არჩილის",
    "ლევან",
    "ლევანის",
    "კონსტანტინე",
    "კონსტანტინეს",
    "სიმონ",
    "სიმონის",
    "ლუარსაბ",
    "ლუარსაბის",
    "მირიან",
    "ფარნავაზ",
    "ნიკოლოზ",
    "ნიკოლოზის",
    "პეტრე",
    "პეტრეს",
    "ნაპოლეონ",
    "ნაპოლეონის",
    "ლუი",
    "ლუის",
    "ჰენრი",
    "ჰენრის",
    "ჩარლზ",
    "ჩარლზის",
    "კარლ",
    "კარლის",
    "ფილიპ",
    "ფილიპის",
    "ფრიდრიხ",
    "ვილჰელმ",
    "ედუარდ",
    "ედუარდის",
    "ელიზაბეტ",
    "ელიზაბეტის",
    "ეკატერინე",
    "ეკატერინეს",
    "Louis",
    "Elizabeth",
    "Charles",
    "Henry",
    "George",
    "Edward",
    "Philip",
    "Wilhelm",
    "Karl",
    "Alexander",
    "Peter",
    "Nicholas",
];
fn build_keywords(list: &[&str]) -> HashSet<String> {
    let mut result = HashSet::with_capacity(list.len() * 2);
    for &word in list {
        result.insert(word.to_owned());
        let mut chars = word.chars();
        if let Some(first) = chars.next() {
            result.insert(format!("{}{}", first.to_uppercase(), chars.as_str().to_lowercase()));
        }
    }
    result
}
static PRE_WORDS: LazyLock<HashSet<String>> = LazyLock::new(|| build_keywords(PRE));
static POST_WORDS: LazyLock<HashSet<String>> = LazyLock::new(|| build_keywords(POST));
static MONARCH_WORDS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| MONARCH.iter().copied().collect());
fn strip_mark(s: &str) -> &str {
    ["-ელი", "-ლი", "-ე", "–ე", "—ე"].iter().find_map(|m| s.strip_suffix(m)).unwrap_or(s)
}
pub fn multiword_start(s: &str) -> bool {
    let roman = strip_mark(s.strip_suffix('.').unwrap_or(s));
    PRE_WORDS.contains(s)
        || MONARCH_WORDS.contains(s)
        || roman_value(roman).is_some()
        || ["§", "№", "No.", "no.", "გვ.", "გვ", "ტ.", "ტ", "სურ.", "ცხრ."].contains(&s)
}
pub fn roman(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let Some((a, b)) = s.split_once(' ') else { return out };
    if PRE_WORDS.contains(a) || a == "§" {
        let a = if a == "§" { "პარაგრაფი" } else { a };
        let r = b.strip_suffix('.').unwrap_or(b);
        if let Some(n) = roman_value(r) {
            if !matches!(r, "C" | "D" | "L" | "M") {
                if let Some(w) = cardinal(&n.to_string()) {
                    out.push((name(&format!("{a} {w}")), 0.0));
                }
            }
        }
        if let Some(n) = roman_value(strip_mark(b)) {
            if let Some(w) = ordinal(&n.to_string()) {
                out.push((name(&format!("{a} {w}")), 0.05));
            }
        }
    }
    if POST_WORDS.contains(b) || matches!(b, "ს." | "სს.") {
        let a = a.strip_suffix('.').unwrap_or(a);
        if let Some(n) = roman_value(strip_mark(a)) {
            if let Some(w) = ordinal(&n.to_string()) {
                let b = match b {
                    "ს." => "საუკუნე",
                    "სს." => "საუკუნეები",
                    _ => b,
                };
                out.push((name(&format!("{w} {b}")), 0.0));
            }
        }
    }
    if MONARCH_WORDS.contains(a) {
        let b = b.strip_suffix('.').unwrap_or(b);
        let b = strip_mark(b);
        let (r, suffix) = split_suffix(b);
        if let Some(n) = roman_value(r).filter(|&n| n <= 30) {
            if let Some(w) = ordinal(&n.to_string()) {
                let w = suffix.map_or(w.clone(), |s| glue_suffix(&w, s));
                out.push((name(&format!("{a} {w}")), 0.0));
            }
        }
    }
    out
}
const ABBREVIATIONS: &[(&str, &str)] = &[
    ("№", "ნომერი"),
    ("No.", "ნომერი"),
    ("no.", "ნომერი"),
    ("გვ.", "გვერდი"),
    ("გვ", "გვერდი"),
    ("ტ.", "ტომი"),
    ("ტ", "ტომი"),
    ("სურ.", "სურათი"),
    ("ცხრ.", "ცხრილი"),
];
const ERAS: &[(&str, &str)] = &[
    ("BC", "ჩვენს ერამდე"),
    ("B.C.", "ჩვენს ერამდე"),
    ("BCE", "ჩვენს ერამდე"),
    ("B.C.E.", "ჩვენს ერამდე"),
    ("ძვ. წ.", "ჩვენს ერამდე"),
    ("ძვ.წ.", "ჩვენს ერამდე"),
    ("AD", "ჩვენი ერით"),
    ("A.D.", "ჩვენი ერით"),
    ("CE", "ჩვენი ერით"),
    ("C.E.", "ჩვენი ერით"),
    ("ახ. წ.", "ჩვენი ერით"),
    ("ახ.წ.", "ჩვენი ერით"),
    ("ჩვ. წ.", "ჩვენი ერით"),
];

/// Membership gate derived from the three composite input branches.
pub fn may_composite(s: &str) -> bool {
    if ABBREVIATIONS.iter().any(|(key, _)| s.starts_with(key)) {
        return true;
    }
    if !s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        return false;
    }
    if s.contains(':') && s.contains(['-', '–', '—', '֊']) {
        return true;
    }
    s.ends_with(['.', 'C', 'D', 'E'])
        && ERAS.iter().any(|(era, _)| s.strip_suffix(era).is_some_and(|prefix| prefix.ends_with(' ')))
}
pub fn composite(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for &(abbr, word) in ABBREVIATIONS {
        if let Some(rest) = s.strip_prefix(abbr) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            if let Some((v, w)) = number(rest).or_else(|| range_weight(rest)) {
                out.push((name(&format!("{word} {v}")), w));
            }
        }
    }
    for &(era, word) in ERAS {
        if let Some(y) = s.strip_suffix(era).and_then(|x| x.strip_suffix(' ')) {
            if y.len() <= 4 {
                if let Some(y) = cardinal(y) {
                    out.push((name(&format!("{word} {y}")), 0.0));
                }
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—' | '֊')) {
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let r = &s[i + c.len_utf8()..];
        let b = r.strip_prefix(' ').unwrap_or(r);
        if a.matches(':').count() == 1 && b.matches(':').count() == 1 {
            if let (Some((_, a)), Some((_, b))) = (datetime::clock(a), datetime::clock(b)) {
                out.push((name(&format!("{} {}", glue_suffix(&a, "დან"), glue_suffix(&b, "მდე"))), 0.0));
            }
        }
    }
    out
}
fn alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || ('\u{10d0}'..='\u{10fa}').contains(&c) || ('\u{1c90}'..='\u{1cba}').contains(&c)
}
pub fn serial(s: &str) -> Option<String> {
    if let Some((base, pow)) = s.split_once('^') {
        let mut chars = base.chars();
        let variable = chars.next().is_some_and(|c| alpha(c) || "αβγδπθλμσω".contains(c))
            && chars.clone().count() <= 3
            && chars.all(alpha);
        let digits = !base.is_empty() && base.len() <= 6 && base.bytes().all(|c| c.is_ascii_digit());
        if matches!(pow, "2" | "3") && (variable || digits) {
            let b = if digits { number(base)?.0 } else { base.to_owned() };
            return Some(format!("cardinal {{ integer: \"{b} {}\" }}", if pow == "2" { "კვადრატში" } else { "კუბში" }));
        }
    }
    let (version, base) = if let Some(b) = s.strip_prefix(['v', 'V']) { (true, b) } else { (false, s) };
    let (dotted, port) = base.split_once(':').map_or((base, None), |(a, b)| (a, Some(b)));
    let pieces: Vec<_> = dotted.split('.').collect();
    if (version && (2..=5).contains(&pieces.len()) || !version && (3..=6).contains(&pieces.len()))
        && pieces.iter().all(|x| !x.is_empty() && x.len() <= 6 && x.bytes().all(|b| b.is_ascii_digit()))
    {
        if version && port.is_some() {
            return None;
        }
        let mut words = pieces.iter().map(|x| number(x).map(|x| x.0)).collect::<Option<Vec<_>>>()?.join(" წერტილი ");
        if let Some(p) = port {
            if p.len() > 6 || !p.bytes().all(|c| c.is_ascii_digit()) {
                return None;
            }
            words.push_str(" ორწერტილი ");
            words.push_str(&number(p)?.0);
        }
        if version {
            words = format!("ვერსია {words}");
        }
        return Some(format!("cardinal {{ integer: \"{words}\" }}"));
    }
    if !s.chars().any(alpha)
        || !s.chars().any(|c| c.is_ascii_digit())
        || !s.chars().all(|c| alpha(c) || c.is_ascii_digit() || matches!(c, '-' | '–' | '—' | '֊' | '/' | '_'))
    {
        return None;
    }
    if s.split(['-', '–', '—', '֊', '/', '_']).any(str::is_empty) {
        return None;
    }
    if let Some((i, c)) = s.char_indices().find(|(_, c)| matches!(c, '-' | '–' | '—' | '֊')) {
        let base = &s[..i];
        let suffix = &s[i + c.len_utf8()..];
        if !base.is_empty()
            && base.bytes().all(|b| b.is_ascii_digit())
            && (suffix.starts_with('ე') || (base == "1" && (suffix.starts_with("ელ") || suffix.starts_with('ლ'))))
        {
            return None;
        }
        if !base.is_empty()
            && base.bytes().all(|b| b.is_ascii_digit())
            && !suffix.is_empty()
            && suffix.chars().all(|c| ('\u{10d0}'..='\u{10fa}').contains(&c) || ('\u{1c90}'..='\u{1cba}').contains(&c))
        {
            return None;
        }
    }
    let mut words = Vec::new();
    let mut chunk = String::new();
    let mut digit = None;
    for c in s.chars().chain(std::iter::once('_')) {
        if matches!(c, '-' | '–' | '—' | '֊' | '/' | '_') {
            if !chunk.is_empty() {
                words.push(if digit == Some(true) {
                    if chunk.len() > 6 {
                        return None;
                    }
                    number(&chunk)?.0
                } else {
                    chunk.clone()
                });
                chunk.clear();
                digit = None;
            }
            continue;
        }
        let d = c.is_ascii_digit();
        if digit.is_some() && digit != Some(d) {
            words.push(if digit == Some(true) {
                if chunk.len() > 6 {
                    return None;
                }
                number(&chunk)?.0
            } else {
                chunk.clone()
            });
            chunk.clear();
        }
        digit = Some(d);
        chunk.push(c);
    }
    Some(format!("cardinal {{ integer: \"{}\" }}", words.join(" ")))
}

fn serial_weight(s: &str) -> f64 {
    let base = s.split('^').next().unwrap_or(s);
    base.split(|c: char| !c.is_ascii_digit()).filter(|part| part.len() > 1 && part.starts_with('0')).count() as f64
        * 0.1
}

pub fn serial_tags(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(tag) = serial(s) {
        out.push((tag, serial_weight(s)));
    }
    let (base, suffix) = split_suffix(s);
    if let Some(suffix) = suffix {
        let dotted = base.contains('.');
        let georgian_end = base
            .chars()
            .last()
            .is_some_and(|c| ('\u{10d0}'..='\u{10fa}').contains(&c) || ('\u{1c90}'..='\u{1cba}').contains(&c));
        if dotted || georgian_end {
            if let Some(tag) = serial(base) {
                if let Some(words) = tag.strip_prefix("cardinal { integer: \"").and_then(|t| t.strip_suffix("\" }")) {
                    out.push((
                        format!("cardinal {{ integer: \"{}\" }}", glue_suffix(words, suffix)),
                        serial_weight(base) - 0.05,
                    ));
                }
            }
        }
    }
    out
}
