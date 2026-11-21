// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese electronic verbalizer: emails, URLs, IP addresses, software versions.

use crate::tag::{Field, Value};

crate::labels!(SYMBOLS_LABELS, "vi/electronic/symbols.tsv");
crate::labels!(DOMAINS_LABELS, "vi/electronic/domain.tsv");
crate::labels!(PROTOCOL_LABELS, "vi/electronic/protocol.tsv");

fn digit_word(c: char) -> Option<&'static str> {
    match c {
        '0' => Some("không"),
        '1' => Some("một"),
        '2' => Some("hai"),
        '3' => Some("ba"),
        '4' => Some("bốn"),
        '5' => Some("năm"),
        '6' => Some("sáu"),
        '7' => Some("bảy"),
        '8' => Some("tám"),
        '9' => Some("chín"),
        _ => None,
    }
}

fn symbol_word(c: char) -> Option<&'static str> {
    for row in SYMBOLS_LABELS.iter() {
        if row.len() >= 2 && row[0].chars().count() == 1 && row[0].starts_with(c) {
            return Some(&row[1]);
        }
    }
    None
}

fn spell_text(s: &str) -> String {
    let mut out = String::new();
    for c in s.chars() {
        if let Some(d) = digit_word(c) {
            out.push(' ');
            out.push_str(d);
            out.push(' ');
        } else if let Some(sym) = symbol_word(c) {
            out.push(' ');
            out.push_str(sym);
            out.push(' ');
        } else {
            out.push(c);
        }
    }
    collapse_spaces(&out)
}

fn spell_domain(s: &str) -> String {
    let mut rewritten = String::new();
    let mut rem = s;
    while !rem.is_empty() {
        let mut matched = None;
        for row in DOMAINS_LABELS.iter() {
            let (Some(key), Some(value)) = (row.first(), row.get(1)) else { continue };
            if let Some(after) = rem.strip_prefix(key.as_str()) {
                if after.is_empty() || matches!(after.chars().next(), Some('/' | '.' | ':')) {
                    matched = Some((after, value.as_str()));
                    break;
                }
            }
        }
        if let Some((after, spoken)) = matched {
            rewritten.push(' ');
            rewritten.push_str(spoken);
            rewritten.push(' ');
            rem = after;
        } else {
            let Some(ch) = rem.chars().next() else { break };
            rewritten.push(ch);
            rem = &rem[ch.len_utf8()..];
        }
    }
    spell_text(&rewritten)
}

fn spell_protocol(proto: &str) -> Option<String> {
    let www = PROTOCOL_LABELS.iter().find(|row| row.first().is_some_and(|s| s == "www."))?.get(1)?;
    for row in PROTOCOL_LABELS.iter() {
        let (Some(key), Some(value)) = (row.first(), row.get(1)) else { continue };
        if key == proto {
            return Some(value.clone());
        }
        if key.starts_with("http") && proto.strip_prefix(key.as_str()) == Some("www.") {
            return Some(format!("{} {}", value, www));
        }
    }
    None
}

fn collapse_spaces(s: &str) -> String {
    let mut out = String::new();
    let mut pending = false;
    for c in s.chars() {
        if crate::text::is_nemo_space(c) {
            pending = !out.is_empty();
        } else {
            if pending {
                out.push(' ');
            }
            out.push(c);
            pending = false;
        }
    }
    out
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let keys = || d.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["value"])
        && !keys().eq(["domain"])
        && !keys().eq(["protocol", "domain"])
        && !keys().eq(["username", "domain"])
    {
        return None;
    }
    let mut value: Option<&str> = None;
    let mut username: Option<&str> = None;
    let mut domain: Option<&str> = None;
    let mut protocol: Option<&str> = None;

    for (k, v) in d {
        match (k.as_ref(), v) {
            ("value", Value::Str(s)) => value = Some(s.as_ref()),
            ("username", Value::Str(s)) => username = Some(s.as_ref()),
            ("domain", Value::Str(s)) => domain = Some(s.as_ref()),
            ("protocol", Value::Str(s)) => protocol = Some(s.as_ref()),
            _ => return None,
        }
    }

    if let Some(v) = value {
        return Some(v.to_string());
    }

    if let (Some(u), Some(dom)) = (username, domain) {
        let u_spelled = spell_text(u);
        let dom_spelled = spell_domain(dom);
        return Some(format!("{u_spelled} a còng {dom_spelled}"));
    }

    if let Some(dom) = domain {
        let dom_spelled = spell_domain(dom);
        if let Some(proto) = protocol {
            let proto_spelled = spell_protocol(proto)?;
            return Some(format!("{proto_spelled} {dom_spelled}"));
        } else {
            return Some(dom_spelled);
        }
    }

    None
}
