use super::graph_utils::{CARDINALS, digit};
use super::numbers::{core, digits};
fn alpha(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphabetic())
}
fn domain(s: &str) -> Option<String> {
    let (a, b) = s.rsplit_once('.')?;
    if alpha(a) && ["dev", "ai", "io", "com", "org", "net", "edu", "gov", "in"].contains(&b) {
        Some(format!("{a} ଡଟ୍ {b}"))
    } else {
        None
    }
}
fn alphanum(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let mut out = String::new();
    let mut first = true;
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            out.push(c);
        } else if let Some(d) = digit(c) {
            if first {
                return None;
            }
            out.push(' ');
            out.push_str(CARDINALS[d as usize]);
        } else {
            return None;
        }
        first = false;
    }
    Some(out)
}
fn email_part(s: &str) -> Option<String> {
    alphanum(s).or_else(|| digits(s))
}
fn symbol(c: char) -> Option<&'static str> {
    Some(match c {
        '.' => " ଡଟ୍ ",
        '@' => " ଆଟ୍ ",
        '/' => " ସ୍ଲାସ୍ ",
        ':' => " କୋଲନ୍ ",
        '-' => " ଡ୍ୟାସ୍ ",
        '_' => " ଅଣ୍ଡରସ୍କୋର ",
        '~' => " ଟିଲ୍ଡେ ",
        '?' => " ପ୍ରଶ୍ନ ଚିହ୍ନ ",
        '=' => " ସମାନ ",
        '&' => " ଏବଂ ",
        '%' => " ପ୍ରତିଶତ ",
        _ => return None,
    })
}
fn url(s: &str) -> Option<String> {
    let (proto, rest) = if let Some(r) = s.strip_prefix("http://") {
        ("http", r)
    } else if let Some(r) = s.strip_prefix("https://") {
        ("https", r)
    } else {
        return None;
    };
    let slash = rest.find('/').unwrap_or(rest.len());
    let head = &rest[..slash];
    let (host, port) = if let Some((h, p)) = head.split_once(':') { (h, Some(p)) } else { (head, None) };
    let hostword = if alpha(host) { host.to_string() } else { domain(host)? };
    let mut out = format!("{proto} କୋଲନ୍ ସ୍ଲାସ୍ ସ୍ଲାସ୍ {hostword}");
    if let Some(p) = port {
        out.push_str(" କୋଲନ୍ ");
        out.push_str(&digits(p)?);
    }
    if slash < rest.len() {
        let path = &rest[slash + 1..];
        if path.is_empty() {
            return None;
        }
        out.push_str(" ସ୍ଲାସ୍ ");
        for c in path.chars() {
            if c.is_ascii_alphabetic() {
                out.push(c);
            } else if let Some(d) = digit(c) {
                out.push(' ');
                out.push_str(CARDINALS[d as usize]);
                out.push(' ');
            } else {
                out.push_str(symbol(c)?);
            }
        }
    }
    Some(out)
}
pub fn electronic(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let power = match s {
        "x^2" | "x²" => Some("ଏକ୍ସ ସ୍କୋୟାର"),
        "y^3" | "y³" => Some("ୱାଇ କ୍ୟୁବ୍"),
        "x^3" => Some("ଏକ୍ସ କ୍ୟୁବ୍"),
        "y^2" => Some("ୱାଇ ସ୍କୋୟାର"),
        _ => None,
    };
    if let Some(w) = power {
        out.push(w.into());
    }
    if let Some(w) = domain(s) {
        out.push(w);
    }
    if let Some(w) = url(s) {
        out.push(w);
    }
    if let Some((local, host)) = s.split_once('@') {
        if let Some(h) = domain(host) {
            let parts: Option<Vec<_>> = local.split('.').map(email_part).collect();
            if let Some(parts) = parts {
                out.push(format!("{} ଆଟ୍ {h}", parts.join(" ଡଟ୍ ")));
            }
        }
    }
    if let Some((prefix, rest)) =
        s.strip_prefix('v').map(|r| ("v", r)).or_else(|| s.strip_prefix('V').map(|r| ("V", r)))
    {
        if let Some((a, b)) = rest.split_once('.') {
            if let (Some(x), Some(y)) = (core(a), core(b)) {
                out.push(format!("{prefix} {x} ଡଟ୍ {y}"));
            }
        }
    }
    let p: Vec<_> = s.split('.').collect();
    if p.len() >= 3 {
        let v: Option<Vec<_>> = p.iter().map(|x| core(x)).collect();
        if let Some(v) = v {
            out.push(v.join(" ଡଟ୍ "));
        }
    }
    let p: Vec<_> = s.split('-').collect();
    if p.len() == 3 && alpha(p[0]) && alpha(p[2]) {
        if let Some(w) = digits(p[1]) {
            out.push(format!("{} {w} {}", p[0], p[2]));
        }
    }
    for (i, _) in s.char_indices().skip(1) {
        let a = &s[..i];
        let b = &s[i..];
        if alpha(a) {
            for (j, _) in b.char_indices().skip(1).chain(std::iter::once((b.len(), ' '))) {
                let x = &b[..j];
                let y = &b[j..];
                if (y.is_empty() || alpha(y)) && !x.is_empty() {
                    if let Some(w) = digits(x) {
                        out.push(if y.is_empty() { format!("{a} {w}") } else { format!("{a} {w} {y}") });
                    }
                }
            }
        }
        if alpha(b) {
            if let Some(w) = digits(a) {
                out.push(format!("{w} {b}"));
            }
        }
    }
    out
}
