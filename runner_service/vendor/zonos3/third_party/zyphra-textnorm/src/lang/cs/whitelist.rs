use super::numbers::cardinal;
use std::collections::HashMap;
use std::sync::LazyLock;
const WORDS: &[(&str, &str)] = &[
    ("např.", "například"),
    ("např", "například"),
    ("Např.", "Například"),
    ("Např", "Například"),
    ("atd.", "a tak dále"),
    ("atd", "a tak dále"),
    ("apod.", "a podobně"),
    ("apod", "a podobně"),
    ("tj.", "to jest"),
    ("tj", "to jest"),
    ("tzn.", "to znamená"),
    ("tzn", "to znamená"),
    ("tzv.", "tak zvaný"),
    ("tzv", "tak zvaný"),
    ("Tzv.", "Tak zvaný"),
    ("Tzv", "Tak zvaný"),
    ("resp.", "respektive"),
    ("resp", "respektive"),
    ("cca", "přibližně"),
    ("Cca", "Přibližně"),
    ("dr.", "doktor"),
    ("Dr.", "doktor"),
    ("Dr", "doktor"),
    ("ing.", "inženýr"),
    ("Ing.", "inženýr"),
    ("Ing", "inženýr"),
    ("prof.", "profesor"),
    ("Prof.", "profesor"),
    ("Prof", "profesor"),
    ("pan", "pan"),
    ("Pan", "Pan"),
    ("pí", "paní"),
    ("pí.", "paní"),
    ("p.", "pan"),
    ("odd.", "oddíl"),
    ("hod.", "hodin"),
    ("min.", "minut"),
    ("tis.", "tisíc"),
    ("mil.", "milionů"),
    ("mld.", "miliard"),
    ("bil.", "bilionů"),
    ("&", "a"),
    ("©", "copyright"),
    ("§", "paragraf"),
    ("®", "registrovaná známka"),
    ("™", "ochranná známka"),
    ("Doc.", "docent"),
    ("doc.", "docent"),
    ("Mgr.", "magistr"),
    ("mgr.", "magistr"),
    ("Bc.", "bakalář"),
    ("bc.", "bakalář"),
];
static WORD_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| WORDS.iter().copied().collect());

pub fn has_word(word: &str) -> bool {
    WORD_INDEX.contains_key(word)
}
const PREFIX: &[(&str, &str)] = &[
    ("č.", "číslo"),
    ("č", "číslo"),
    ("Č.", "Číslo"),
    ("Č", "Číslo"),
    ("čís.", "číslo"),
    ("No.", "číslo"),
    ("No", "číslo"),
    ("#", "číslo"),
    ("str.", "strana"),
    ("str", "strana"),
    ("s.", "strana"),
    ("§", "paragraf"),
    ("©", "copyright"),
];
/// A whitelist expression can cross a space only after a numeric prefix.
pub fn may_cross_space(first: &str) -> bool {
    PREFIX.iter().any(|(prefix, _)| first.starts_with(*prefix))
}
pub fn whitelist(s: &str) -> Option<String> {
    if let Some(v) = WORD_INDEX.get(s) {
        return Some((*v).to_owned());
    }
    for &(k, v) in PREFIX {
        if let Some(n) = s.strip_prefix(k) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if let Some(n) = cardinal(n) {
                return Some(format!("{v} {n}"));
            }
        }
    }
    None
}
