//! Productive Tagalog number and linker morphology.
//!
//! The spelling rules follow `tl/taggers/cardinal.py` and `tl/graph_utils.py`.

const DIGITS: [&str; 10] = ["wala", "isa", "dalawa", "tatlo", "apat", "lima", "anim", "pito", "walo", "siyam"];
const TEENS: [&str; 10] = [
    "sampu",
    "labing-isa",
    "labindalawa",
    "labintatlo",
    "labing-apat",
    "labinlima",
    "labing-anim",
    "labimpito",
    "labingwalo",
    "labinsiyam",
];
const TENS: [&str; 10] =
    ["", "sampu", "dalawampu", "tatlumpu", "apatnapu", "limampu", "animnapu", "pitumpu", "walumpu", "siyamnapu"];
const HUNDREDS: [&str; 10] = [
    "",
    "isang daan",
    "dalawang daan",
    "tatlong daan",
    "apat na daan",
    "limang daan",
    "anim na daan",
    "pitong daan",
    "walong daan",
    "siyam na daan",
];

/// Apply the source grammar's phonological linker to a spoken number.
pub fn link_in_place(word: &mut String) -> Option<()> {
    match word.chars().last()? {
        'a' | 'e' | 'i' | 'o' | 'u' | 'A' | 'E' | 'I' | 'O' | 'U' => word.push_str("ng"),
        'n' | 'N' => {
            word.pop();
            word.push_str("ng");
        }
        'b'..='m' | 'p'..='z' | 'B'..='M' | 'P'..='Z' => word.push_str(" na"),
        _ => return None,
    }
    Some(())
}

fn three(out: &mut String, n: usize) {
    let hundreds = n / 100;
    let rest = n % 100;
    out.push_str(HUNDREDS[hundreds]);
    if hundreds != 0 && rest != 0 {
        out.push_str(" at ");
    }
    if rest < 10 {
        if rest != 0 {
            out.push_str(DIGITS[rest]);
        }
    } else if rest < 20 {
        out.push_str(TEENS[rest - 10]);
    } else {
        out.push_str(TENS[rest / 10]);
        if rest % 10 != 0 {
            out.push_str("'t ");
            out.push_str(DIGITS[rest % 10]);
        }
    }
}

fn spell(mut value: u64) -> String {
    let mut groups = [0usize; 5];
    for group in &mut groups {
        *group = (value % 1000) as usize;
        value /= 1000;
    }
    let scales = ["", "libo", "milyon", "bilyon", "trilyon"];
    let mut out = String::new();
    for i in (0..5).rev() {
        if groups[i] == 0 {
            continue;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        three(&mut out, groups[i]);
        if i != 0 {
            match out.as_bytes().last().copied() {
                Some(b'a' | b'e' | b'i' | b'o' | b'u') => out.push_str("ng"),
                Some(b'n') => {
                    out.pop();
                    out.push_str("ng");
                }
                _ => out.push_str(" na"),
            }
            out.push(' ');
            out.push_str(scales[i]);
        }
    }
    out
}

/// Spell the ungrouped, non-leading-zero cardinal graph, bounded to 15 digits.
pub fn cardinal(input: &str) -> Option<String> {
    if input == "0" {
        return Some(DIGITS[0].to_owned());
    }
    if input.is_empty() || input.len() > 15 || input.starts_with('0') || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    Some(spell(input.parse().ok()?))
}

/// The cardinal graph also accepts groups of three separated by commas or dots.
pub fn cardinal_with_sep(input: &str) -> Option<String> {
    if !input.contains([',', '.']) {
        return cardinal(input);
    }
    let mut groups = input.split([',', '.']);
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut value: u64 = first.parse().ok()?;
    let mut count = 0;
    for group in groups {
        count += 1;
        if count > 4 || group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        value = value * 1000 + group.parse::<u64>().ok()?;
    }
    if !(1..=4).contains(&count) {
        return None;
    }
    Some(spell(value))
}

/// The independent digit graph used in fractions, decimals, and telephone numbers.
pub fn digit_sequence(input: &str) -> Option<String> {
    if input.is_empty() {
        return None;
    }
    if !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut out = String::new();
    for b in input.bytes() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(DIGITS[(b - b'0') as usize]);
    }
    Some(out)
}
