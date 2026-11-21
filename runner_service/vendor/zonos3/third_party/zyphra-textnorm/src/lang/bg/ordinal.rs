use super::graph_utils::{Gender, cardinal};

pub(crate) fn ordinal(s: &str, g: Gender) -> Option<String> {
    if s.is_empty() || s.len() > 4 || !s.bytes().all(|b| b.is_ascii_digit()) || s.starts_with('0') {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n == 0 {
        return None;
    }
    let end = match g {
        Gender::Masculine => "и",
        Gender::Feminine => "а",
        Gender::Neuter => "о",
    };
    let stems = ["", "първ", "втор", "трет", "четвърт", "пет", "шест", "седм", "осм", "девет"];
    let teens = [
        "десет",
        "единадесет",
        "дванадесет",
        "тринадесет",
        "четиринадесет",
        "петнадесет",
        "шестнадесет",
        "седемнадесет",
        "осемнадесет",
        "деветнадесет",
    ];
    let tens = [
        "",
        "",
        "двадесет",
        "тридесет",
        "четиридесет",
        "петдесет",
        "шестдесет",
        "седемдесет",
        "осемдесет",
        "деветдесет",
    ];
    let hs =
        ["", "стот", "двестот", "тристот", "четиристот", "петстот", "шестстот", "седемстот", "осемстот", "деветстот"];
    let part = |v: usize| -> String {
        if v < 10 {
            return format!("{}{end}", stems[v]);
        }
        if v < 20 {
            return format!("{}{end}", teens[v - 10]);
        }
        if v < 100 {
            if v % 10 == 0 {
                return format!("{}{end}", tens[v / 10]);
            }
            return format!("{} и {}{end}", tens[v / 10], stems[v % 10]);
        }
        if v % 100 == 0 {
            return format!(
                "{}{}",
                hs[v / 100],
                match g {
                    Gender::Masculine => "ен",
                    Gender::Feminine => "на",
                    Gender::Neuter => "но",
                }
            );
        }
        let prefix = cardinal(&(v / 100 * 100).to_string(), Gender::Neuter).unwrap();
        let r = v % 100;
        let sep = if r < 20 || r % 10 == 0 { " и " } else { " " };
        let suffix = if r < 10 {
            format!("{}{end}", stems[r])
        } else if r < 20 {
            format!("{}{end}", teens[r - 10])
        } else if r % 10 == 0 {
            format!("{}{end}", tens[r / 10])
        } else {
            format!("{} и {}{end}", tens[r / 10], stems[r % 10])
        };
        format!("{prefix}{sep}{suffix}")
    };
    if n < 1000 {
        return Some(part(n));
    }
    let t = n / 1000;
    let rem = n % 1000;
    if rem == 0 {
        let word = match g {
            Gender::Masculine => "хиляден",
            Gender::Feminine => "хилядна",
            Gender::Neuter => "хилядно",
        };
        return Some(if t == 1 {
            word.to_owned()
        } else {
            format!("{} {word}", cardinal(&t.to_string(), Gender::Feminine)?)
        });
    }
    let prefix = if t == 1 {
        "хиляда".to_owned()
    } else {
        format!("{} хиляди", cardinal(&t.to_string(), Gender::Feminine)?)
    };
    let sep = if rem < 20 || rem < 100 && rem % 10 == 0 || rem % 100 == 0 { " и " } else { " " };
    Some(format!("{prefix}{sep}{}", part(rem)))
}

/// Final characters of the source's ordinal suffixes, decades, and dot branch.
pub(crate) fn possible_end(c: char) -> bool {
    matches!(c, '.' | 's' | 'е' | 'и' | 'а' | 'о' | 'н')
}

/// Alphabet used by numeric ordinal suffixes, the dot branch, and decade keys.
pub(crate) fn possible_char(c: char) -> bool {
    c.is_ascii_digit()
        || matches!(c, '-' | '.' | ' ' | '\'' | 's' | 'в' | 'и' | 'р' | 'т' | 'м' | 'е' | 'н' | 'а' | 'о' | 'г' | 'д')
}

pub(crate) fn classify(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let decades = [
        ("90-те", "деветдесетте години"),
        ("1990-те", "деветдесетте години"),
        ("1990-те години", "деветдесетте години"),
        ("1990s", "деветдесетте години"),
        ("1990's", "деветдесетте години"),
        ("2020-те", "двадесетте години на двадесет и първи век"),
        ("2020-те години", "двадесетте години на двадесет и първи век"),
        ("2020s", "двадесетте години на двадесет и първи век"),
        ("2020's", "двадесетте години на двадесет и първи век"),
        ("80-те", "осемдесетте години"),
        ("1980-те", "осемдесетте години"),
        ("1980s", "осемдесетте години"),
        ("70-те", "седемдесетте години"),
        ("1970-те", "седемдесетте години"),
        ("1970s", "седемдесетте години"),
        ("60-те", "шестдесетте години"),
        ("1960-те", "шестдесетте години"),
        ("1960s", "шестдесетте години"),
    ];
    for (a, b) in decades {
        if s == a {
            out.push((b.to_owned(), 1.0));
        }
    }
    for (suffix, g) in [
        ("ви", Gender::Masculine),
        ("ри", Gender::Masculine),
        ("ти", Gender::Masculine),
        ("ми", Gender::Masculine),
        ("тен", Gender::Masculine),
        ("ен", Gender::Masculine),
        ("ва", Gender::Feminine),
        ("ра", Gender::Feminine),
        ("та", Gender::Feminine),
        ("ма", Gender::Feminine),
        ("тна", Gender::Feminine),
        ("на", Gender::Feminine),
        ("во", Gender::Neuter),
        ("ро", Gender::Neuter),
        ("то", Gender::Neuter),
        ("мо", Gender::Neuter),
        ("тно", Gender::Neuter),
        ("но", Gender::Neuter),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            if let Some(v) = ordinal(n.strip_suffix('-').unwrap_or(n), g) {
                out.push((v, 1.05));
            }
        }
    }
    if let Some(n) = s.strip_suffix('.') {
        if let Some(v) = ordinal(n, Gender::Masculine) {
            out.push((v, 1.1));
        }
    }
    out
}
