use super::number::positive;
const MONTHS: [&str; 12] = [
    "januára",
    "februára",
    "marca",
    "apríla",
    "mája",
    "júna",
    "júla",
    "augusta",
    "septembra",
    "októbra",
    "novembra",
    "decembra",
];
const WRITTEN: [&str; 24] = [
    "januára",
    "január",
    "februára",
    "február",
    "marca",
    "marec",
    "apríla",
    "apríl",
    "mája",
    "máj",
    "júna",
    "jún",
    "júla",
    "júl",
    "augusta",
    "august",
    "septembra",
    "september",
    "októbra",
    "október",
    "novembra",
    "november",
    "decembra",
    "december",
];
pub(super) fn written_months() -> &'static [&'static str] {
    &WRITTEN
}
const DAYS: [&str; 31] = [
    "prvého",
    "druhého",
    "tretieho",
    "štvrtého",
    "piateho",
    "šiesteho",
    "siedmeho",
    "ôsmeho",
    "deviateho",
    "desiateho",
    "jedenásteho",
    "dvanásteho",
    "trinásteho",
    "štrnásteho",
    "pätnásteho",
    "šestnásteho",
    "sedemnásteho",
    "osemnásteho",
    "devätnásteho",
    "dvadsiateho",
    "dvadsiateho prvého",
    "dvadsiateho druhého",
    "dvadsiateho tretieho",
    "dvadsiateho štvrtého",
    "dvadsiateho piateho",
    "dvadsiateho šiesteho",
    "dvadsiateho siedmeho",
    "dvadsiateho ôsmeho",
    "dvadsiateho deviateho",
    "tridsiateho",
    "tridsiateho prvého",
];
fn index(s: &str, max: usize) -> Option<usize> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let x: usize = s.parse().ok()?;
    if x == 0 || x > max {
        return None;
    }
    Some(x - 1)
}
fn suffix(s: &str) -> (&str, bool) {
    for x in [" roku", "г.", "g.", "r.", "."] {
        if let Some(y) = s.strip_suffix(x) {
            return (y, true);
        }
    }
    (s, false)
}
pub(super) fn iso(s: &str) -> Option<String> {
    let (year, rest) = s.split_once('-')?;
    let (month, day) = rest.split_once('-')?;
    if year.len() != 4 || month.len() != 2 || day.len() != 2 {
        return None;
    }
    let y = positive(year)?;
    let m = MONTHS[index(month, 12)?];
    let d = DAYS[index(day, 31)?];
    Some(spoken!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"))
}
pub(super) fn date(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    date_each(s, |value, weight| out.push((value, weight)));
    out
}
pub(super) fn date_each(s: &str, mut emit: impl FnMut(String, f64)) {
    if let Some((day, rest)) = s.split_once(' ') {
        let d = day.strip_suffix('.').unwrap_or(day);
        if let Some(di) = index(d, 31) {
            let (month, year) = rest.split_once(' ').map_or((rest, None), |(m, y)| (m, Some(y)));
            if WRITTEN.contains(&month) {
                if let Some(year) = year {
                    let year = if let Some(year) = year.strip_suffix(" roku") { year } else { suffix(year).0 };
                    if let Some(y) = positive(year) {
                        emit(spoken!("{} {} {y}", DAYS[di], month), 0.06);
                    }
                } else {
                    emit(spoken!("{} {}", DAYS[di], month), 0.08);
                }
            }
        }
    }
    for sep in ['.', '/'] {
        if let Some((d, rest)) = s.split_once(sep) {
            if let Some((m, rest)) = rest.split_once(sep) {
                let m = m.strip_prefix(' ').unwrap_or(m);
                let y = rest.strip_prefix(' ').unwrap_or(rest);
                let (y, has_suf) = suffix(y);
                if let (Some(di), Some(mi), Some(year)) = (index(d, 31), index(m, 12), positive(y)) {
                    if y.len() == 2 || y.len() == 4 || has_suf {
                        emit(spoken!("{} {} {year}", DAYS[di], MONTHS[mi]), 0.07);
                    }
                }
            }
        }
    }
    for prefix in ["v roku ", "v "] {
        if let Some(x) = s.strip_prefix(prefix) {
            let x = x.strip_suffix(" roku").or_else(|| x.strip_suffix(" r.")).unwrap_or(x);
            if let Some(y) = positive(x) {
                emit(spoken!("v roku {y}"), 0.09);
            }
        }
    }
    for (i, c) in s.char_indices() {
        if "-–—".contains(c) {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            if a.len() == 4 && b.len() == 4 {
                if let (Some(x), Some(y)) = (positive(a), positive(b)) {
                    emit(spoken!("{x} až {y}"), 0.09);
                }
            }
        }
    }
    for (suffix, word) in [
        (" pred n. l.", "pred naším letopočtom"),
        (" pred n.l.", "pred naším letopočtom"),
        (" BC", "pred naším letopočtom"),
        (" BCE", "pred naším letopočtom"),
        (" n. l.", "nášho letopočtu"),
        (" n.l.", "nášho letopočtu"),
        (" AD", "nášho letopočtu"),
        (" CE", "nášho letopočtu"),
    ] {
        if let Some(x) = s.strip_suffix(suffix).and_then(positive) {
            emit(spoken!("{x} {word}"), 0.05);
        }
    }
    for suf in ["-te", "s", ". roky"] {
        if let Some(x) = s.strip_suffix(suf) {
            let name = match x {
                "0" => Some("nulté"),
                "10" => Some("desiate"),
                "20" => Some("dvadsiate"),
                "30" => Some("tridsiate"),
                "40" => Some("štyridsiate"),
                "50" => Some("päťdesiate"),
                "60" => Some("šesťdesiate"),
                "70" => Some("sedemdesiate"),
                "80" => Some("osemdesiate"),
                "90" => Some("deväťdesiate"),
                _ => None,
            };
            if let Some(name) = name {
                emit(spoken!("{name} roky"), 0.05);
            }
        }
    }
    for suf in ["-te", "s", " roky", "-roky"] {
        if let Some(x) = s.strip_suffix(suf) {
            if x.len() == 4 && x.ends_with('0') {
                if let Some(y) = positive(x) {
                    emit(spoken!("desaťročie od roku {y}"), 0.05);
                }
            }
        }
    }
}
