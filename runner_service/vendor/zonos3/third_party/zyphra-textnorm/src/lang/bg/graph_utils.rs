//! Bulgarian cardinal morphology from `bg/taggers/cardinal.py`.
//!
//! The source uses different genders for the final group and for scale prefixes.

#[derive(Clone, Copy)]
pub(crate) enum Gender {
    Neuter,
    Masculine,
    Feminine,
}

const TEENS: [&str; 10] = [
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
const TENS: [&str; 10] =
    ["", "", "двадесет", "тридесет", "четиридесет", "петдесет", "шестдесет", "седемдесет", "осемдесет", "деветдесет"];
const HUNDREDS: [&str; 10] = [
    "",
    "сто",
    "двеста",
    "триста",
    "четиристотин",
    "петстотин",
    "шестстотин",
    "седемстотин",
    "осемстотин",
    "деветстотин",
];

fn unit(n: usize, gender: Gender) -> &'static str {
    match (n, gender) {
        (0, _) => "нула",
        (1, Gender::Masculine) => "един",
        (1, Gender::Feminine) => "една",
        (1, Gender::Neuter) => "едно",
        (2, Gender::Masculine) => "два",
        (2, _) => "две",
        (3, _) => "три",
        (4, _) => "четири",
        (5, _) => "пет",
        (6, _) => "шест",
        (7, _) => "седем",
        (8, _) => "осем",
        (9, _) => "девет",
        _ => unreachable!(),
    }
}

fn sub_thousand(result: &mut String, n: usize, gender: Gender) {
    let h = n / 100;
    let rest = n % 100;
    if h != 0 {
        result.push_str(HUNDREDS[h]);
        if rest != 0 {
            result.push_str(if rest < 20 || rest % 10 == 0 { " и " } else { " " });
        }
    }
    match rest {
        0 => {}
        1..=9 => result.push_str(unit(rest, gender)),
        10..=19 => result.push_str(TEENS[rest - 10]),
        _ => {
            result.push_str(TENS[rest / 10]);
            if rest % 10 != 0 {
                result.push_str(" и ");
                result.push_str(unit(rest % 10, gender));
            }
        }
    }
}

/// Accepts the ungrouped cardinal input language: zero or a nonzero number of
/// at most twelve ASCII digits. Leading zeroes are a separate source branch.
pub(crate) fn cardinal(input: &str, gender: Gender) -> Option<String> {
    if input.is_empty() || input.len() > 12 || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if input.len() > 1 && input.starts_with('0') {
        return None;
    }
    let mut n: usize = input.parse().ok()?;
    if n == 0 {
        return Some("нула".to_owned());
    }
    let mut groups = [0; 4];
    for group in &mut groups {
        *group = n % 1000;
        n /= 1000;
    }
    // The billion remainder graph has no path for the `001` million group.
    if groups[3] != 0 && groups[2] == 1 {
        return None;
    }
    let mut out = String::with_capacity(input.len() * 20 + 16);
    for i in (0..4).rev() {
        let value = groups[i];
        if value == 0 {
            continue;
        }
        if !out.is_empty() {
            if groups[3] != 0 && groups[2] == 0 && (i == 1 || i == 0 && groups[1] == 0) {
                out.push(' ');
            }
            let exact_thousand = i == 1 && value == 1 && groups[0] == 0;
            if (i == 0 && (value < 20 || value < 100 && value % 10 == 0 || value % 100 == 0)) || exact_thousand {
                out.push_str(" и ");
            } else {
                out.push(' ');
                if i == 1 && value == 1 && groups[0] != 0 && (groups[2] != 0 || groups[3] != 0) {
                    out.push(' ');
                }
            }
        }
        match i {
            0 => {
                sub_thousand(&mut out, value, if groups[1..].iter().any(|&v| v != 0) { Gender::Neuter } else { gender })
            }
            1 if value == 1 => out.push_str("хиляда"),
            1 => {
                sub_thousand(&mut out, value, Gender::Feminine);
                out.push_str(" хиляди");
            }
            2 | 3 => {
                let scale_gender = if (i == 3 || groups[3] == 0) && value >= 100 && value % 100 == 1 {
                    Gender::Feminine
                } else {
                    Gender::Masculine
                };
                sub_thousand(&mut out, value, scale_gender);
                out.push_str(match (i, value) {
                    (2, 1) => " милион",
                    (2, _) => " милиона",
                    (3, 1) => " милиард",
                    _ => " милиарда",
                });
            }
            _ => unreachable!(),
        }
    }
    Some(out)
}
