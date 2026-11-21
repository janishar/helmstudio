use super::graph_utils::{CARDINALS, cardinal, digit};

pub fn digits(s: &str) -> Option<String> {
    let mut words = Vec::new();
    for c in s.chars() {
        words.push(CARDINALS[digit(c)? as usize]);
    }
    if words.is_empty() { None } else { Some(words.join(" ")) }
}

fn grouped(s: &str) -> Option<String> {
    if !s.contains([',', ' ']) {
        return None;
    }
    let mut parts = s.split([',', ' ']);
    let first = parts.next()?;
    if first.is_empty() || first.chars().next().and_then(digit) == Some(0) {
        return None;
    }
    let first_width = first.chars().count();
    if !first.chars().all(|c| digit(c).is_some()) {
        return None;
    }
    let mut western = (1..=3).contains(&first_width);
    let mut indian = (1..=2).contains(&first_width);
    let mut previous = None;
    for part in parts {
        let width = part.chars().count();
        if width == 0 || !part.chars().all(|c| digit(c).is_some()) {
            return None;
        }
        western &= width == 3;
        if let Some(prev) = previous {
            indian &= prev == 2;
        }
        previous = Some(width);
    }
    let last = previous?;
    if !western && !(indian && last == 3) {
        return None;
    }
    Some(s.chars().filter(|c| *c != ',' && *c != ' ').collect())
}

pub fn core(s: &str) -> Option<String> {
    if let Some(normalized) = grouped(s) { cardinal(&normalized) } else { cardinal(s) }
}

pub fn cardinal_forms(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if s.chars().count() > 1 && s.chars().next().and_then(digit) == Some(0) {
        if let Some(w) = digits(s) {
            out.push((w, -50_000));
        }
    }
    if let Some(w) = core(s) {
        out.push((w, 0));
    }
    for (suffix, spoken) in [("ଟି", "ଟି"), ("ଟା", "ଟା"), ("ଜଣ", " ଜଣ"), ("ଖଣ୍ଡ", " ଖଣ୍ଡ")]
    {
        if let Some(rest) = s.strip_suffix(suffix) {
            if let Some(w) = core(rest) {
                out.push((format!("{w}{spoken}"), -100_000));
            }
        }
    }
    for (prefix, spoken) in [("-", "ଋଣ "), ("−", "ଋଣ "), ("~", "ପ୍ରାୟ "), ("±", "ପ୍ଲସ୍ ମାଇନସ୍ ")]
    {
        if let Some(rest) = s.strip_prefix(prefix) {
            if let Some(w) = core(rest) {
                out.push((format!("{spoken}{w}"), -100_000));
            }
            if prefix == "~" || prefix == "±" {
                for (suffix, v) in [("ଟି", "ଟି"), ("ଟା", "ଟା"), ("ଜଣ", " ଜଣ"), ("ଖଣ୍ଡ", " ଖଣ୍ଡ")]
                {
                    if let Some(r) = rest.strip_suffix(suffix) {
                        if let Some(w) = core(r) {
                            out.push((format!("{spoken}{w}{v}"), -100_000));
                        }
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') && i > 0 {
            if let (Some(a), Some(b)) = (core(&s[..i]), core(&s[i + c.len_utf8()..])) {
                out.push((format!("{a} ରୁ {b}"), 0));
            }
        }
    }
    out
}

pub fn decimal(s: &str) -> Option<String> {
    if grouped(s).is_some() {
        return None;
    }
    let (prefix, raw) = if let Some(r) = s.strip_prefix('~') {
        ("ପ୍ରାୟ ", r)
    } else if let Some(r) = s.strip_prefix('±') {
        ("ପ୍ଲସ୍ ମାଇନସ୍ ", r)
    } else {
        ("", s)
    };
    let (sign, raw) = if let Some(r) = raw.strip_prefix('-').or_else(|| raw.strip_prefix('−')) {
        ("ଋଣ ", r)
    } else {
        ("", raw)
    };
    let mut scale = None;
    let mut base = raw;
    for (k, v) in [
        ("କୋଟି", "କୋଟି"),
        ("ଲକ୍ଷ", "ଲକ୍ଷ"),
        ("ହଜାର", "ହଜାର"),
        ("ମିଲିଅନ୍", "ମିଲିଅନ୍"),
        ("ମିଲିୟନ", "ମିଲିଅନ୍"),
        ("ବିଲିଅନ୍", "ବିଲିଅନ୍"),
        ("ବିଲିୟନ", "ବିଲିଅନ୍"),
        ("k", "ହଜାର"),
        ("K", "ହଜାର"),
        ("M", "ମିଲିଅନ୍"),
        ("B", "ବିଲିଅନ୍"),
    ] {
        if let Some(r) = raw.strip_suffix(k) {
            base = r.strip_suffix(' ').unwrap_or(r);
            scale = Some(v);
            break;
        }
    }
    let result = if let Some(i) = base.rfind(['.', ',']) {
        let a = &base[..i];
        let b = &base[i + 1..];
        if a.is_empty() && (!prefix.is_empty() || scale.is_some()) {
            return None;
        }
        let left = if a.is_empty() { "ଶୂନ୍ୟ".to_string() } else { core(a)? };
        format!("{left} ଦଶମିକ {}", digits(b)?)
    } else {
        if scale.is_none() {
            return None;
        }
        core(base)?
    };
    Some(format!("{prefix}{sign}{result}{}", scale.map(|v| format!(" {v}")).unwrap_or_default()))
}

pub fn ordinal(s: &str) -> Option<String> {
    const ORD: [&str; 10] = ["ପ୍ରଥମ", "ଦ୍ୱିତୀୟ", "ତୃତୀୟ", "ଚତୁର୍ଥ", "ପଞ୍ଚମ", "ଷଷ୍ଠ", "ସପ୍ତମ", "ଅଷ୍ଟମ", "ନବମ", "ଦଶମ"];
    for (suf, n) in [("ମ", 1), ("ୟ", 2), ("ର୍ଥ", 4), ("ଷ୍ଠ", 6), ("st", 1), ("nd", 2), ("rd", 3), ("th", 0)]
    {
        if let Some(a) = s.strip_suffix(suf) {
            let Some(ds): Option<Vec<_>> = a.chars().map(digit).collect() else { continue };
            let val = ds.iter().fold(0usize, |v, d| v.saturating_mul(10).saturating_add(*d as usize));
            let special = match suf {
                "ମ" => [1, 5, 7, 8, 9, 10].contains(&val),
                "ୟ" => val == 2 || val == 3,
                "ର୍ଥ" => val == 4,
                "ଷ୍ଠ" => val == 6,
                "th" => (4..=10).contains(&val),
                _ => val == n,
            };
            if special && ds.len() == if val == 10 { 2 } else { 1 } {
                return Some(ORD[val - 1].to_string());
            }
        }
    }
    for suf in ["ତମ", "th", "st", "nd", "rd"] {
        if let Some(a) = s.strip_suffix(suf) {
            return Some(format!("{}ତମ", cardinal(a)?));
        }
    }
    None
}

pub fn ordinal_forms(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(w) = ordinal(s) {
        out.push(w);
    }
    for suf in ["ତମ", "th", "st", "nd", "rd"] {
        if let Some(a) = s.strip_suffix(suf) {
            if let Some(w) = cardinal(a) {
                let w = format!("{w}ତମ");
                if !out.contains(&w) {
                    out.push(w);
                }
            }
        }
    }
    out
}

fn simple_fraction(s: &str) -> Option<String> {
    let named = match s {
        "½" | "1/2" | "୧/୨" => Some("ଅଧା"),
        "¼" | "1/4" | "୧/୪" => Some("ଚଉଠ"),
        "¾" | "3/4" | "୩/୪" => Some("ତିନି ଚତୁର୍ଥାଂଶ"),
        "⅓" | "1/3" | "୧/୩" => Some("ଏକ ତୃତୀୟାଂଶ"),
        "⅔" | "2/3" | "୨/୩" => Some("ଦୁଇ ତୃତୀୟାଂଶ"),
        "⅕" | "1/5" | "୧/୫" => Some("ଏକ ପଞ୍ଚମାଂଶ"),
        "⅛" => Some("ଏକ ଅଷ୍ଟମାଂଶ"),
        "⅝" | "5/8" | "୫/୮" => Some("ପାଞ୍ଚ ଅଷ୍ଟମାଂଶ"),
        "⅞" | "7/8" | "୭/୮" => Some("ସାତ ଅଷ୍ଟମାଂଶ"),
        "⅒" | "1/10" | "୧/୧୦" => Some("ଏକ ଦଶମାଂଶ"),
        _ => None,
    };
    if let Some(w) = named {
        return Some(w.to_string());
    }
    generic_fraction(s)
}

fn generic_fraction(s: &str) -> Option<String> {
    let (a, b) = s.split_once('/')?;
    let n: Vec<_> = b.chars().map(digit).collect::<Option<_>>()?;
    let den = match n.as_slice() {
        [2] => "ଦ୍ୱିତୀୟାଂଶ",
        [3] => "ତୃତୀୟାଂଶ",
        [4] => "ଚତୁର୍ଥାଂଶ",
        [5] => "ପଞ୍ଚମାଂଶ",
        [6] => "ଷଷ୍ଠାଂଶ",
        [7] => "ସପ୍ତମାଂଶ",
        [8] => "ଅଷ୍ଟମାଂଶ",
        [9] => "ନବମାଂଶ",
        [1, 0] => "ଦଶମାଂଶ",
        _ => return None,
    };
    Some(format!("{} {den}", cardinal(a)?))
}

pub fn fraction(s: &str) -> Option<String> {
    let (neg, s) =
        if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("ଋଣ ", r) } else { ("", s) };
    if let Some(w) = simple_fraction(s) {
        return Some(format!("{neg}{w}"));
    }
    for (sym, kind) in
        [("1/2", 0), ("୧/୨", 0), ("½", 0), ("1/4", 1), ("୧/୪", 1), ("¼", 1), ("3/4", 2), ("୩/୪", 2), ("¾", 2)]
    {
        if let Some(a) = s.strip_suffix(sym) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            if let Some(w) = cardinal(a) {
                let n: Vec<_> = a.chars().map(digit).collect::<Option<_>>()?;
                let val = if n.len() == 1 { n[0] as usize } else { 100 };
                let result = match kind {
                    0 if val == 1 => "ଦେଢ଼".to_string(),
                    0 if val == 2 => "ଅଢ଼େଇ".to_string(),
                    0 => format!("ସାଢ଼େ {w}"),
                    1 => format!("ସୱା {w}"),
                    2 if (1..=9).contains(&val) => format!("ପୌଣେ {}", CARDINALS[val + 1]),
                    _ => continue,
                };
                return Some(format!("{neg}{result}"));
            }
        }
    }
    if let Some((a, b)) = s.split_once(' ') {
        return Some(format!("{neg}{} ପୂର୍ଣ୍ଣ {}", cardinal(a)?, simple_fraction(b)?));
    }
    None
}

pub fn fraction_forms(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(w) = fraction(s) {
        out.push(w);
    }
    let (neg, raw) =
        if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("ଋଣ ", r) } else { ("", s) };
    if let Some(w) = generic_fraction(raw) {
        let w = format!("{neg}{w}");
        if !out.contains(&w) {
            out.push(w);
        }
    }
    for sym in ["1/2", "୧/୨", "½"] {
        if let Some(a) = raw.strip_suffix(sym) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            if let Some(w) = cardinal(a) {
                let w = format!("{neg}ସାଢ଼େ {w}");
                if !out.contains(&w) {
                    out.push(w);
                }
            }
        }
    }
    if let Some((a, b)) = raw.split_once(' ') {
        if let Some(x) = cardinal(a) {
            for y in [simple_fraction(b), generic_fraction(b)].into_iter().flatten() {
                let w = format!("{neg}{x} ପୂର୍ଣ୍ଣ {y}");
                if !out.contains(&w) {
                    out.push(w);
                }
            }
        }
    }
    out
}
