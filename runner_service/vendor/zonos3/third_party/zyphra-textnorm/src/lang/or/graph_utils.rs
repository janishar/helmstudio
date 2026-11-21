// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
// Primitive vocabulary and scale rules from or/taggers/cardinal.py.

pub(crate) const CARDINALS: [&str; 100] = [
    "ଶୂନ୍ୟ",
    "ଏକ",
    "ଦୁଇ",
    "ତିନି",
    "ଚାରି",
    "ପାଞ୍ଚ",
    "ଛଅ",
    "ସାତ",
    "ଆଠ",
    "ନଅ",
    "ଦଶ",
    "ଏଗାର",
    "ବାର",
    "ତେର",
    "ଚଉଦ",
    "ପନ୍ଦର",
    "ଷୋଳ",
    "ସତର",
    "ଅଠର",
    "ଊଣେଇଶ",
    "କୋଡ଼ିଏ",
    "ଏକୋଇଶ",
    "ବାଇଶ",
    "ତେଇଶ",
    "ଚଉବିଶ",
    "ପଚିଶ",
    "ଛବିଶ",
    "ସତାଇଶ",
    "ଅଠାଇଶ",
    "ଊଣାତିରିଶ",
    "ତିରିଶ",
    "ଏକତିରିଶ",
    "ବତିଶ",
    "ତେତିଶ",
    "ଚଉତିରିଶ",
    "ପଞ୍ଚତିରିଶ",
    "ଛତିଶ",
    "ସତତିରିଶ",
    "ଅଠତିରିଶ",
    "ଊଣଚାଳିଶ",
    "ଚାଳିଶ",
    "ଏକଚାଳିଶ",
    "ବୟାଳିଶ",
    "ତେତାଳିଶ",
    "ଚଉରାଳିଶ",
    "ପଞ୍ଚଚାଳିଶ",
    "ଛେଚାଳିଶ",
    "ସତଚାଳିଶ",
    "ଅଠଚାଳିଶ",
    "ଊଣପଚାଶ",
    "ପଚାଶ",
    "ଏକାବନ",
    "ବାବନ",
    "ତେପନ",
    "ଚଉବନ",
    "ପଞ୍ଚାବନ",
    "ଛପନ",
    "ସତାବନ",
    "ଅଠାବନ",
    "ଊଣଷାଠିଏ",
    "ଷାଠିଏ",
    "ଏକଷଠି",
    "ବାଷଠି",
    "ତେଷଠି",
    "ଚଉଷଠି",
    "ପଞ୍ଚଷଠି",
    "ଛଅଷଠି",
    "ସତଷଠି",
    "ଅଠଷଠି",
    "ଊଣସତୁରୀ",
    "ସତୁରୀ",
    "ଏକସତୁରୀ",
    "ବାସତୁରୀ",
    "ତେସତୁରୀ",
    "ଚଉସତୁରୀ",
    "ପଞ୍ଚସତୁରୀ",
    "ଛଅସତୁରୀ",
    "ସତସତୁରୀ",
    "ଅଠସତୁରୀ",
    "ଊଣାଶୀ",
    "ଅଶୀ",
    "ଏକାଶୀ",
    "ବୟାଶୀ",
    "ତେରାଶୀ",
    "ଚଉରାଶୀ",
    "ପଞ୍ଚାଶୀ",
    "ଛିଆଶୀ",
    "ସତାଶୀ",
    "ଅଠାଶୀ",
    "ଊଣାନବେ",
    "ନବେ",
    "ଏକାନବେ",
    "ବୟାନବେ",
    "ତେରାନବେ",
    "ଚଉରାନବେ",
    "ପଞ୍ଚାନବେ",
    "ଛିଆନବେ",
    "ସତାନବେ",
    "ଅଠାନବେ",
    "ନିଆଁନବେ",
];

pub(crate) fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '୦'..='୯' => Some((c as u32 - '୦' as u32) as u8),
        _ => None,
    }
}

/// The ungrouped positive cardinal graph, including zero, but not leading zeros.
pub(crate) fn cardinal(input: &str) -> Option<String> {
    let mut n = 0u64;
    let mut count = 0;
    for c in input.chars() {
        let d = digit(c)?;
        if count == 0 && d == 0 && input.chars().count() != 1 {
            return None;
        }
        n = n.checked_mul(10)?.checked_add(d as u64)?;
        count += 1;
    }
    if count == 0 || count > 13 {
        return None;
    }
    let mut out = String::new();
    spell_into(&mut out, n, " ", false);
    Some(out)
}

/// Write a recognized cardinal directly, optionally using the competing
/// `ଏକ ଶହ` reading of the exactly-one-hundred subgraph.
pub(crate) fn spell_into(out: &mut String, n: u64, space: &str, alternate_hundred: bool) {
    if n < 100 {
        out.push_str(CARDINALS[n as usize]);
        return;
    }
    if n < 1000 {
        if n == 100 && !alternate_hundred {
            out.push_str("ଶହେ");
            return;
        }
        let h = n / 100;
        let r = n % 100;
        out.push_str(CARDINALS[h as usize]);
        out.push_str(space);
        out.push_str("ଶହ");
        if r != 0 {
            out.push_str(space);
            out.push_str(CARDINALS[r as usize]);
        }
        return;
    }
    for (scale, name) in
        [(100_000_000_000u64, "ଖରବ"), (1_000_000_000, "ଅରବ"), (10_000_000, "କୋଟି"), (100_000, "ଲକ୍ଷ"), (1000, "ହଜାର")]
    {
        if n >= scale {
            out.push_str(CARDINALS[(n / scale) as usize]);
            out.push_str(space);
            out.push_str(name);
            let remainder = n % scale;
            if remainder != 0 {
                out.push_str(space);
                spell_into(out, remainder, space, alternate_hundred);
            }
            return;
        }
    }
}
