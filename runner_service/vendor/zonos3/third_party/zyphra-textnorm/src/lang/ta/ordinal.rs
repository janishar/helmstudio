//! Productive Tamil ordinal morphology.
use super::number::{cardinal, digit};
use std::sync::LazyLock;

static ORDINALS: LazyLock<[&'static str; 99]> = LazyLock::new(|| {
    let mut values = [""; 99];
    for (i, line) in include_str!("../../../data/ta/ordinal/ordinals.tsv").lines().enumerate() {
        if i == values.len() {
            break;
        }
        if let Some((_, value)) = line.split_once('\t') {
            values[i] = value;
        }
    }
    values
});
fn under_100(n: usize, compound: bool) -> String {
    if n == 1 && compound {
        return "ஒன்றாவது".into();
    }
    ORDINALS[n - 1].to_owned()
}
fn number(n: u64, compound: bool) -> String {
    if n < 100 {
        return under_100(n as usize, compound);
    }
    if n < 1000 {
        const EXACT: [&str; 10] = [
            "",
            "நூறாவது",
            "இருநூறாவது",
            "முந்நூறாவது",
            "நானூறாவது",
            "ஐநூறாவது",
            "அறுநூறாவது",
            "எழுநூறாவது",
            "எண்நூறாவது",
            "தொள்ளாயிரத்தாவது",
        ];
        const COMB: [&str; 10] =
            ["", "நூற்று", "இருநூற்று", "முந்நூற்று", "நானூற்று", "ஐநூற்று", "அறுநூற்று", "எழுநூற்று", "எண்நூற்று", "தொள்ளாயிரத்து"];
        let h = (n / 100) as usize;
        let r = n % 100;
        return if r == 0 { EXACT[h].into() } else { format!("{} {}", COMB[h], number(r, true)) };
    }
    let (scale, exact, comb) = if n < 100_000 {
        (1000, "த்தாவது", "த்து")
    } else if n < 10_000_000 {
        (100_000, "லட்சத்தாவது", "லட்சத்து")
    } else {
        (10_000_000, "கோடியாவது", "கோடியே")
    };
    let head = n / scale;
    let rest = n % scale;
    if scale == 1000 {
        let base = cardinal(&(head * 1000).to_string()).unwrap().0;
        if head == 1 && rest == 0 {
            return "ஆயிரமாவது".into();
        }
        let stem = base.strip_suffix("ம்").unwrap();
        return if rest == 0 { format!("{stem}{exact}") } else { format!("{stem}{comb} {}", number(rest, true)) };
    }
    let h = if head == 1 { "ஒரு".to_owned() } else { cardinal(&head.to_string()).unwrap().0 };
    if rest == 0 { format!("{h} {exact}") } else { format!("{h} {comb} {}", number(rest, true)) }
}
fn value(s: &str) -> Option<u64> {
    let input =
        ["-ஆவது", "-வது", "ஆவது", "வது", "st", "nd", "rd", "th"].iter().find_map(|suffix| s.strip_suffix(*suffix))?;
    let mut chars = input.chars();
    let first = digit(chars.next()?)?;
    if first == 0 {
        return None;
    }
    let mut n = first as u64;
    let mut len = 1;
    for c in chars {
        len += 1;
        if len > 9 {
            return None;
        }
        n = n * 10 + digit(c)? as u64;
    }
    Some(n)
}

pub fn recognize(s: &str) -> bool {
    value(s).is_some()
}

pub fn classify(s: &str) -> Option<String> {
    Some(number(value(s)?, false))
}
