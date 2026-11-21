//! Contextual Roman numerals, monarchs, centuries, wars and chapters.
use super::graph_utils::{is_positive, ordinal, positive};
fn roman(s: &str) -> Option<usize> {
    if s.len() > 5 || !matches!(s.as_bytes().first(), Some(b'I' | b'V' | b'X')) {
        return None;
    }
    [
        "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
        "XVIII", "XIX", "XX", "XXI",
    ]
    .iter()
    .position(|v| *v == s)
    .map(|i| i + 1)
}
/// The last component must be a contextual noun/name or a Roman numeral.
pub fn possible_end(s: &str) -> bool {
    let Some((_, last)) = s.rsplit_once(' ') else { return false };
    roman(last).is_some()
        || matches!(
            last,
            "yüzyıl"
                | "yüzyılda"
                | "yy."
                | "yy"
                | "Savaşı"
                | "Mehmed"
                | "Mehmet"
                | "Murat"
                | "Louis"
                | "Elizabeth"
                | "Süleyman"
                | "Selim"
                | "Ahmet"
                | "Osman"
                | "Mahmut"
                | "İskender"
                | "Nikolay"
                | "Wilhelm"
                | "Edward"
                | "George"
                | "Henry"
                | "Charles"
                | "Philip"
                | "Paul"
                | "Pius"
                | "Jean"
                | "Kral"
                | "Çar"
                | "Papa"
                | "Sultan"
        )
}
pub fn count(s: &str) -> usize {
    let mut total = 0;
    for (i, _) in s.match_indices(' ') {
        let (raw, b) = (&s[..i], &s[i + 1..]);
        let a = raw.strip_suffix('.').unwrap_or(raw);
        let r = roman(a).is_some();
        if r || is_positive(a) {
            total += usize::from(matches!(b, "yüzyıl" | "yüzyılda" | "yy." | "yy"));
            if r {
                total += usize::from(matches!(
                    b,
                    "Dünya Savaşı"
                        | "Mehmed"
                        | "Mehmet"
                        | "Murat"
                        | "Louis"
                        | "Elizabeth"
                        | "Süleyman"
                        | "Selim"
                        | "Ahmet"
                        | "Osman"
                        | "Mahmut"
                        | "İskender"
                        | "Nikolay"
                        | "Wilhelm"
                        | "Edward"
                        | "George"
                        | "Henry"
                        | "Charles"
                        | "Philip"
                        | "Paul"
                        | "Pius"
                        | "Jean"
                        | "Kral"
                        | "Çar"
                        | "Papa"
                        | "Sultan"
                ));
            }
        }
        if roman(b).is_some() {
            total += usize::from(matches!(
                raw,
                "Bölüm"
                    | "bölüm"
                    | "Chapter"
                    | "chapter"
                    | "Cilt"
                    | "cilt"
                    | "Volume"
                    | "volume"
                    | "Kısım"
                    | "kısım"
                    | "Part"
                    | "part"
                    | "Fasikül"
                    | "fasikül"
                    | "Madde"
                    | "madde"
            ));
            total += 2 * usize::from(matches!(
                raw,
                "Louis" | "Elizabeth" | "Mustafa" | "Mehmet" | "Murat" | "Henry" | "George" | "Edward" | "Charles"
            ));
        }
    }
    total
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (i, _) in s.match_indices(' ') {
        let (a, b) = (&s[..i], &s[i + 1..]);
        let raw_a = a;
        let a = a.strip_suffix('.').unwrap_or(a);
        let number = roman(a).map(|n| n.to_string()).or_else(|| positive(a).map(|_| a.to_string()));
        if let Some(n) = number {
            if let Some(ord) = ordinal(&n) {
                for (x, y) in [("yüzyıl", "yüzyıl"), ("yüzyılda", "yüzyılda"), ("yy.", "yüzyıl"), ("yy", "yüzyıl")]
                {
                    if b == x {
                        out.push(format!("{ord} {y}"));
                    }
                }
                if roman(a).is_some() {
                    if b == "Dünya Savaşı" {
                        out.push(format!("{ord} dünya savaşı"));
                    }
                    for (x, y) in [
                        ("Mehmed", "mehmed"),
                        ("Mehmet", "mehmet"),
                        ("Murat", "murat"),
                        ("Louis", "louis"),
                        ("Elizabeth", "elizabeth"),
                        ("Süleyman", "süleyman"),
                        ("Selim", "selim"),
                        ("Ahmet", "ahmet"),
                        ("Osman", "osman"),
                        ("Mahmut", "mahmut"),
                        ("İskender", "iskender"),
                        ("Nikolay", "nikolay"),
                        ("Wilhelm", "wilhelm"),
                        ("Edward", "edward"),
                        ("George", "george"),
                        ("Henry", "henry"),
                        ("Charles", "charles"),
                        ("Philip", "philip"),
                        ("Paul", "paul"),
                        ("Pius", "pius"),
                        ("Jean", "jean"),
                        ("Kral", "kral"),
                        ("Çar", "çar"),
                        ("Papa", "papa"),
                        ("Sultan", "sultan"),
                    ] {
                        if b == x {
                            out.push(format!("{ord} {y}"));
                        }
                    }
                }
            }
        }
        let a = raw_a;
        if let Some(n) = roman(b) {
            let card = positive(&n.to_string()).unwrap();
            let ord = ordinal(&n.to_string()).unwrap();
            for (x, y) in [
                ("Bölüm", "bölüm"),
                ("bölüm", "bölüm"),
                ("Chapter", "bölüm"),
                ("chapter", "bölüm"),
                ("Cilt", "cilt"),
                ("cilt", "cilt"),
                ("Volume", "cilt"),
                ("volume", "cilt"),
                ("Kısım", "kısım"),
                ("kısım", "kısım"),
                ("Part", "kısım"),
                ("part", "kısım"),
                ("Fasikül", "fasikül"),
                ("fasikül", "fasikül"),
                ("Madde", "madde"),
                ("madde", "madde"),
            ] {
                if a == x {
                    out.push(format!("{y} {card}"));
                }
            }
            for (x, y) in [
                ("Louis", "louis"),
                ("Elizabeth", "elizabeth"),
                ("Mustafa", "mustafa"),
                ("Mehmet", "mehmet"),
                ("Murat", "murat"),
                ("Henry", "henry"),
                ("George", "george"),
                ("Edward", "edward"),
                ("Charles", "charles"),
            ] {
                if a == x {
                    out.push(format!("{y} {ord}"));
                    out.push(format!("{y} {card}"));
                }
            }
        }
    }
    out
}
