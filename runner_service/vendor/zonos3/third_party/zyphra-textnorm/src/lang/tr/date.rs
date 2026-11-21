//! Date and era branches from `tr/taggers/date.py`.
use super::graph_utils::{is_positive, positive};
const MONTHS: [&str; 12] =
    ["ocak", "şubat", "mart", "nisan", "mayıs", "haziran", "temmuz", "ağustos", "eylül", "ekim", "kasım", "aralık"];
/// Last characters of numeric dates, decade suffixes, era labels and every
/// source month name/abbreviation (including their optional trailing dots).
pub fn possible_end(c: char) -> bool {
    c.is_ascii_digit()
        || matches!(
            c,
            '.' | 'a'
                | 'b'
                | 'r'
                | 's'
                | 't'
                | 'n'
                | 'z'
                | 'l'
                | 'm'
                | 'u'
                | 'i'
                | 'y'
                | 'k'
                | 'Ö'
                | 'S'
                | 'C'
                | 'E'
                | 'D'
        )
}
/// Letter runs admitted after a digit-led date are the month/era lexemes or
/// their dot-delimited components, plus the English decade suffix.
pub fn numeric_word(word: &str, complete: bool) -> bool {
    const WORDS: &[&str] = &[
        "Ocak", "ocak", "Oca", "Şubat", "şubat", "Şub", "Mart", "mart", "Mar", "Nisan", "nisan", "Nis", "Mayıs",
        "mayıs", "May", "Haziran", "haziran", "Haz", "Temmuz", "temmuz", "Tem", "Ağustos", "ağustos", "Ağu", "Eylül",
        "eylül", "Eyl", "Ekim", "ekim", "Eki", "Kasım", "kasım", "Kas", "Aralık", "aralık", "Ara", "MÖ", "MS", "BC",
        "BCE", "AD", "CE", "M", "Ö", "S", "B", "C", "E", "A", "D", "s",
    ];
    WORDS.iter().any(|item| if complete { *item == word } else { item.starts_with(word) })
}

fn valid_day(s: &str) -> bool {
    s.len() <= 2 && s.bytes().all(|c| c.is_ascii_digit()) && s.parse::<u8>().ok().is_some_and(|n| n > 0 && n <= 39)
}
fn valid_year(s: &str) -> bool {
    s.len() <= 4 && s.bytes().all(|c| c.is_ascii_digit()) && is_positive(s)
}
fn era(s: &str) -> bool {
    matches!(s, "MÖ" | "M.Ö." | "BC" | "B.C." | "BCE" | "B.C.E." | "MS" | "M.S." | "AD" | "A.D." | "CE" | "C.E.")
}
/// Visit accepted paths in the same order as `readings`; ISO paths have an
/// internal negative weight and are structurally tagged.
pub fn visit(s: &str, mut emit: impl FnMut(bool)) {
    let mut fields = s.split(' ');
    let first = fields.next().unwrap_or("");
    let second = fields.next();
    let third = fields.next();
    if fields.next().is_none() {
        if let Some(b) = second {
            if valid_day(first) && written_month(b).is_some() {
                if third.is_none() || third.is_some_and(valid_year) {
                    emit(false);
                }
            }
            if third.is_none() && ((era(first) && valid_year(b)) || (valid_year(first) && era(b))) {
                emit(false);
            }
        }
    }
    let mut parts = s.split(|c| matches!(c, '.' | '/' | '-'));
    if let (Some(a), Some(b), Some(c)) = (parts.next(), parts.next(), parts.next()) {
        if parts.next().is_none() {
            if valid_day(a) && month(b).is_some() && valid_year(c) {
                emit(false);
            }
            if s.matches('-').count() == 2 && a.len() == 4 && valid_year(a) && month(b).is_some() && valid_day(c) {
                emit(true);
            }
        }
    }
    let dm = match s.strip_suffix('.') {
        Some(v) => v.strip_suffix(' ').unwrap_or(v),
        None => s,
    };
    if let Some((d, m)) = dm.split_once('.') {
        if valid_day(d) && month(m).is_some() {
            emit(false);
        }
    }
    if matches!(s, "1980s" | "1990s" | "2000s" | "2010s" | "2020s" | "80s" | "90s") {
        emit(false);
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if a.len() == 4 && b.len() == 4 && valid_year(a) && valid_year(b) {
                emit(false);
            }
        }
    }
}
fn day(s: &str) -> Option<String> {
    if s.len() > 2 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n == 0 || n > 39 {
        return None;
    }
    positive(&n.to_string())
}
fn month(s: &str) -> Option<&'static str> {
    if s.len() > 2 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n == 0 || n > 12 || (s.len() == 2 && s.starts_with('0') && n >= 10) {
        return None;
    }
    Some(MONTHS[n - 1])
}
fn written_month(s: &str) -> Option<&'static str> {
    Some(match s {
        "Ocak" | "ocak" | "Oca." | "Oca" => "ocak",
        "Şubat" | "şubat" | "Şub." | "Şub" => "şubat",
        "Mart" | "mart" | "Mar." | "Mar" => "mart",
        "Nisan" | "nisan" | "Nis." | "Nis" => "nisan",
        "Mayıs" | "mayıs" | "May." | "May" => "mayıs",
        "Haziran" | "haziran" | "Haz." | "Haz" => "haziran",
        "Temmuz" | "temmuz" | "Tem." | "Tem" => "temmuz",
        "Ağustos" | "ağustos" | "Ağu." | "Ağu" => "ağustos",
        "Eylül" | "eylül" | "Eyl." | "Eyl" => "eylül",
        "Ekim" | "ekim" | "Eki." | "Eki" => "ekim",
        "Kasım" | "kasım" | "Kas." | "Kas" => "kasım",
        "Aralık" | "aralık" | "Ara." | "Ara" => "aralık",
        _ => return None,
    })
}
fn year(s: &str) -> Option<String> {
    if s.len() > 4 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    positive(s)
}
/// Returns ordinary spoken-name branches, and the distinct ISO structured tag.
pub fn readings(s: &str) -> Vec<(String, bool)> {
    let mut out = Vec::new();
    let fields: Vec<&str> = s.split(' ').collect();
    if fields.len() == 2 || fields.len() == 3 {
        if let (Some(d), Some(m)) = (day(fields[0]), written_month(fields[1])) {
            if fields.len() == 2 {
                out.push((format!("{d} {m}"), false));
            } else if let Some(y) = year(fields[2]) {
                out.push((format!("{d} {m} {y}"), false));
            }
        }
        let era = |s: &str| match s {
            "MÖ" | "M.Ö." | "BC" | "B.C." | "BCE" | "B.C.E." => Some("milattan önce"),
            "MS" | "M.S." | "AD" | "A.D." | "CE" | "C.E." => Some("milattan sonra"),
            _ => None,
        };
        if fields.len() == 2 {
            if let (Some(e), Some(y)) = (era(fields[0]), year(fields[1])) {
                out.push((format!("{e} {y}"), false));
            }
            if let (Some(y), Some(e)) = (year(fields[0]), era(fields[1])) {
                out.push((format!("{e} {y}"), false));
            }
        }
    }
    let parts: Vec<&str> = s.split(|c| matches!(c, '.' | '/' | '-')).collect();
    if parts.len() == 3 {
        if let (Some(d), Some(m), Some(y)) = (day(parts[0]), month(parts[1]), year(parts[2])) {
            out.push((format!("{d} {m} {y}"), false));
        }
        if s.contains('-') && s.matches('-').count() == 2 && parts[0].len() == 4 {
            if let (Some(y), Some(m), Some(d)) = (year(parts[0]), month(parts[1]), day(parts[2])) {
                out.push((format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"), true));
            }
        }
    }
    let dm = match s.strip_suffix('.') {
        Some(v) => v.strip_suffix(' ').unwrap_or(v),
        None => s,
    };
    if let Some((d, m)) = dm.split_once('.') {
        if let (Some(d), Some(m)) = (day(d), month(m)) {
            out.push((format!("{d} {m}"), false));
        }
    }
    for (w, v) in [
        ("1980s", "bin dokuz yüz seksenler"),
        ("1990s", "bin dokuz yüz doksanlar"),
        ("2000s", "iki binler"),
        ("2010s", "iki bin onlar"),
        ("2020s", "iki bin yirmiler"),
        ("80s", "seksenler"),
        ("90s", "doksanlar"),
    ] {
        if s == w {
            out.push((v.into(), false));
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if a.len() == 4 && b.len() == 4 {
                if let (Some(a), Some(b)) = (year(a), year(b)) {
                    out.push((format!("{a} ila {b}"), false));
                }
            }
        }
    }
    out
}
