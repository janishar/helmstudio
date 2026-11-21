//! Telugu number spellout rules, matching CLDR %spellout-cardinal and %spellout-ordinal.

pub const DIGITS: [&str; 10] = ["సున్నా", "ఒకటి", "రెండు", "మూడు", "నాలుగు", "ఐదు", "ఆరు", "ఏడు", "ఎనిమిది", "తొమ్మిది"];

const TEENS_AND_TIES: [&str; 90] = [
    // 10..19
    "పది",
    "పదకొండు",
    "పన్నెండు",
    "పదమూడు",
    "పద్నాలుగు",
    "పదిహేను",
    "పదహారు",
    "పదిహేడు",
    "పద్దెనిమిది",
    "పంతొమ్మిది",
    // 20..29
    "ఇరవై",
    "ఇరవై ఒకటి",
    "ఇరవై రెండు",
    "ఇరవై మూడు",
    "ఇరవై నాలుగు",
    "ఇరవై ఐదు",
    "ఇరవై ఆరు",
    "ఇరవై ఏడు",
    "ఇరవై ఎనిమిది",
    "ఇరవై తొమ్మిది",
    // 30..39
    "ముప్పై",
    "ముప్పై ఒకటి",
    "ముప్పై రెండు",
    "ముప్పై మూడు",
    "ముప్పై నాలుగు",
    "ముప్పై ఐదు",
    "ముప్పై ఆరు",
    "ముప్పై ఏడు",
    "ముప్పై ఎనిమిది",
    "ముప్పై తొమ్మిది",
    // 40..49
    "నలభై",
    "నలభై ఒకటి",
    "నలభై రెండు",
    "నలభై మూడు",
    "నలభై నాలుగు",
    "నలభై ఐదు",
    "నలభై ఆరు",
    "నలభై ఏడు",
    "నలభై ఎనిమిది",
    "నలభై తొమ్మిది",
    // 50..59
    "యాభై",
    "యాభై ఒకటి",
    "యాభై రెండు",
    "యాభై మూడు",
    "యాభై నాలుగు",
    "యాభై ఐదు",
    "యాభై ఆరు",
    "యాభై ఏడు",
    "యాభై ఎనిమిది",
    "యాభై తొమ్మిది",
    // 60..69
    "అరవై",
    "అరవై ఒకటి",
    "అరవై రెండు",
    "అరవై మూడు",
    "అరవై నాలుగు",
    "అరవై ఐదు",
    "అరవై ఆరు",
    "అరవై ఏడు",
    "అరవై ఎనిమిది",
    "అరవై తొమ్మిది",
    // 70..79
    "డెబ్బై",
    "డెబ్బై ఒకటి",
    "డెబ్బై రెండు",
    "డెబ్బై మూడు",
    "డెబ్బై నాలుగు",
    "డెబ్బై ఐదు",
    "డెబ్బై ఆరు",
    "డెబ్బై ఏడు",
    "డెబ్బై ఎనిమిది",
    "డెబ్బై తొమ్మిది",
    // 80..89
    "ఎనభై",
    "ఎనభై ఒకటి",
    "ఎనభై రెండు",
    "ఎనభై మూడు",
    "ఎనభై నాలుగు",
    "ఎనభై ఐదు",
    "ఎనభై ఆరు",
    "ఎనభై ఏడు",
    "ఎనభై ఎనిమిది",
    "ఎనభై తొమ్మిది",
    // 90..99
    "తొంభై",
    "తొంభై ఒకటి",
    "తొంభై రెండు",
    "తొంభై మూడు",
    "తొంభై నాలుగు",
    "తొంభై ఐదు",
    "తొంభై ఆరు",
    "తొంభై ఏడు",
    "తొంభై ఎనిమిది",
    "తొంభై తొమ్మిది",
];

const ORDINALS_1_99: [&str; 99] = [
    "మొదటి",
    "రెండవ",
    "మూడవ",
    "నాలుగవ",
    "ఐదవ",
    "ఆరవ",
    "ఏడవ",
    "ఎనిమిదవ",
    "తొమ్మిదవ",
    "పదవ",
    "పదకొండవ",
    "పన్నెండవ",
    "పదమూడవ",
    "పద్నాలుగవ",
    "పదిహేనవ",
    "పదహారవ",
    "పదిహేడవ",
    "పద్దెనిమిదవ",
    "పంతొమ్మిదవ",
    "ఇరవయ్యవ",
    "ఇరవై ఒకటవ",
    "ఇరవై రెండవ",
    "ఇరవై మూడవ",
    "ఇరవై నాలుగవ",
    "ఇరవై ఐదవ",
    "ఇరవై ఆరవ",
    "ఇరవై ఏడవ",
    "ఇరవై ఎనిమిదవ",
    "ఇరవై తొమ్మిదవ",
    "ముప్పయ్యవ",
    "ముప్పై ఒకటవ",
    "ముప్పై రెండవ",
    "ముప్పై మూడవ",
    "ముప్పై నాలుగవ",
    "ముప్పై ఐదవ",
    "ముప్పై ఆరవ",
    "ముప్పై ఏడవ",
    "ముప్పై ఎనిమిదవ",
    "ముప్పై తొమ్మిదవ",
    "నలభయ్యవ",
    "నలభై ఒకటవ",
    "నలభై రెండవ",
    "నలభై మూడవ",
    "నలభై నాలుగవ",
    "నలభై ఐదవ",
    "నలభై ఆరవ",
    "నలభై ఏడవ",
    "నలభై ఎనిమిదవ",
    "నలభై తొమ్మిదవ",
    "యాభయ్యవ",
    "యాభై ఒకటవ",
    "యాభై రెండవ",
    "యాభై మూడవ",
    "యాభై నాలుగవ",
    "యాభై ఐదవ",
    "యాభై ఆరవ",
    "యాభై ఏడవ",
    "యాభై ఎనిమిదవ",
    "యాభై తొమ్మిదవ",
    "అరవయ్యవ",
    "అరవై ఒకటవ",
    "అరవై రెండవ",
    "అరవై మూడవ",
    "అరవై నాలుగవ",
    "అరవై ఐదవ",
    "అరవై ఆరవ",
    "అరవై ఏడవ",
    "అరవై ఎనిమిదవ",
    "అరవై తొమ్మిదవ",
    "డెబ్బయ్యవ",
    "డెబ్బై ఒకటవ",
    "డెబ్బై రెండవ",
    "డెబ్బై మూడవ",
    "డెబ్బై నాలుగవ",
    "డెబ్బై ఐదవ",
    "డెబ్బై ఆరవ",
    "డెబ్బై ఏడవ",
    "డెబ్బై ఎనిమిదవ",
    "డెబ్బై తొమ్మిదవ",
    "ఎనభయ్యవ",
    "ఎనభై ఒకటవ",
    "ఎనభై రెండవ",
    "ఎనభై మూడవ",
    "ఎనభై నాలుగవ",
    "ఎనభై ఐదవ",
    "ఎనభై ఆరవ",
    "ఎనభై ఏడవ",
    "ఎనభై ఎనిమిదవ",
    "ఎనభై తొమ్మిదవ",
    "తొంభయ్యవ",
    "తొంభై ఒకటవ",
    "తొంభై రెండవ",
    "తొంభై మూడవ",
    "తొంభై నాలుగవ",
    "తొంభై ఐదవ",
    "తొంభై ఆరవ",
    "తొంభై ఏడవ",
    "తొంభై ఎనిమిదవ",
    "తొంభై తొమ్మిదవ",
];

#[inline]
pub fn digit_val(c: char) -> Option<u32> {
    if c.is_ascii_digit() {
        Some(c as u32 - '0' as u32)
    } else if ('\u{0c66}'..='\u{0c6f}').contains(&c) {
        Some(c as u32 - '\u{0c66}' as u32)
    } else {
        None
    }
}

#[inline]
pub fn is_digit(c: char) -> bool {
    digit_val(c).is_some()
}

#[inline]
pub fn is_zero(c: char) -> bool {
    c == '0' || c == '\u{0c66}'
}

pub fn digit_word(c: char) -> Option<&'static str> {
    digit_val(c).map(|v| DIGITS[v as usize])
}

pub fn spell_two_digits(n: u32) -> Option<&'static str> {
    if n == 0 {
        None
    } else if (1..=9).contains(&n) {
        Some(DIGITS[n as usize])
    } else if (10..=99).contains(&n) {
        Some(TEENS_AND_TIES[(n - 10) as usize])
    } else {
        None
    }
}

fn append_up_to_999(out: &mut String, n: u32) {
    if n < 100 {
        out.push_str(spell_two_digits(n).unwrap());
        return;
    }
    let h = n / 100;
    let rem = n % 100;
    if h == 1 {
        out.push_str(if rem == 0 { "వంద" } else { "నూట" });
    } else {
        out.push_str(DIGITS[h as usize]);
        out.push_str(if rem == 0 { " వందలు" } else { " వందల" });
    }
    if rem != 0 {
        out.push(' ');
        out.push_str(spell_two_digits(rem).unwrap());
    }
}

pub fn spell_up_to_999(n: u32) -> Option<String> {
    if n == 0 {
        return None;
    }
    let mut out = String::new();
    append_up_to_999(&mut out, n);
    Some(out)
}

pub fn spell_cardinal(n: u64) -> String {
    let mut out = String::new();
    if n == 0 {
        out.push_str("సున్నా");
    } else {
        append_positive(&mut out, n);
    }
    out
}

fn append_positive(out: &mut String, n: u64) {
    if n < 1000 {
        append_up_to_999(out, n as u32);
        return;
    }
    let (scale, singular, plural) = if n >= 100_000_000_000 {
        (100_000_000_000, "ఖరబ్", "ఖరబ్")
    } else if n >= 1_000_000_000 {
        (1_000_000_000, "అరబ్", "అరబ్")
    } else if n >= 10_000_000 {
        (10_000_000, "ఒక కోటి", "కోట్ల")
    } else if n >= 100_000 {
        (100_000, "ఒక లక్ష", "లక్షల")
    } else {
        (1_000, "వెయ్యి", "వేల")
    };
    let head = n / scale;
    let rem = n % scale;
    if head == 1 {
        out.push_str(singular);
    } else {
        append_positive(out, head);
        out.push(' ');
        out.push_str(plural);
    }
    if rem != 0 {
        out.push(' ');
        append_positive(out, rem);
    }
}

pub fn parse_digits_u64(s: &str) -> Option<u64> {
    if s.is_empty() {
        return None;
    }
    let mut val: u64 = 0;
    for c in s.chars() {
        let d = digit_val(c)? as u64;
        val = val.checked_mul(10)?.checked_add(d)?;
    }
    Some(val)
}

pub fn cardinal_into(out: &mut String, s: &str) -> Option<()> {
    if s.is_empty() || s.chars().count() > 18 {
        return None;
    }
    if s == "0" || s == "\u{0c66}" {
        out.push_str("సున్నా");
        return Some(());
    }
    let first = s.chars().next()?;
    if is_zero(first) {
        return None;
    }
    let n = parse_digits_u64(s)?;
    append_positive(out, n);
    Some(())
}

pub fn cardinal(s: &str) -> Option<String> {
    let mut out = String::new();
    cardinal_into(&mut out, s)?;
    Some(out)
}

pub fn to_attributive(s: &str) -> String {
    let res = if let Some(stripped) = s.strip_suffix("వందలు") {
        format!("{stripped}వందల")
    } else {
        s.to_string()
    };
    if res == "ఒకటి" {
        "ఒక".to_string()
    } else if let Some(stripped) = res.strip_suffix(" ఒకటి") {
        format!("{stripped} ఒక")
    } else {
        res
    }
}

pub fn digitwise(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push_str(digit_word(c)?);
    }
    Some(out)
}

pub fn code_num(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let char_count = s.chars().count();
    if char_count == 1 {
        digitwise(s)
    } else if char_count <= 3 {
        let first = s.chars().next()?;
        if is_zero(first) { None } else { cardinal(s) }
    } else {
        digitwise(s)
    }
}

pub fn spell_ordinal(n: u64) -> Option<String> {
    if n == 0 {
        return None;
    }
    if n <= 99 {
        return Some(ORDINALS_1_99[(n - 1) as usize].to_string());
    }
    if n < 1000 {
        let h = (n / 100) as u32;
        let rem = (n % 100) as u32;
        return if h == 1 {
            if rem == 0 {
                Some("వందవ".to_string())
            } else {
                let ro = spell_ordinal(rem as u64)?;
                Some(format!("నూట {ro}"))
            }
        } else {
            let h_s = DIGITS[h as usize];
            if rem == 0 {
                Some(format!("{h_s} వందలవ"))
            } else {
                let ro = spell_ordinal(rem as u64)?;
                Some(format!("{h_s} వందల {ro}"))
            }
        };
    }
    if n < 100_000 {
        let t = n / 1000;
        let rem = n % 1000;
        return if t == 1 {
            if rem == 0 {
                Some("వెయ్యవ".to_string())
            } else {
                let ro = spell_ordinal(rem)?;
                Some(format!("వెయ్యి {ro}"))
            }
        } else {
            let t_s = spell_cardinal(t);
            if rem == 0 {
                Some(format!("{t_s} వేలవ"))
            } else {
                let ro = spell_ordinal(rem)?;
                Some(format!("{t_s} వేల {ro}"))
            }
        };
    }
    if n < 10_000_000 {
        let l = n / 100_000;
        let rem = n % 100_000;
        return if l == 1 {
            if rem == 0 {
                Some("ఒక లక్షవ".to_string())
            } else {
                let ro = spell_ordinal(rem)?;
                Some(format!("ఒక లక్ష {ro}"))
            }
        } else {
            let l_s = spell_cardinal(l);
            if rem == 0 {
                Some(format!("{l_s} లక్షలవ"))
            } else {
                let ro = spell_ordinal(rem)?;
                Some(format!("{l_s} లక్షల {ro}"))
            }
        };
    }
    None
}
