// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use super::s;
use crate::tag::Field;

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let country = s(d, i, "country_code");
    if country.is_some() {
        i += 1;
    }
    let number = s(d, i, "number_part")?;
    i += 1;
    let extension = s(d, i, "extension");
    if extension.is_some() {
        i += 1;
    }
    if i != d.len()
        || number.is_empty()
        || number.contains('"')
        || country.is_some_and(|s| s.is_empty() || s.contains('"'))
        || extension.is_some_and(|s| s.is_empty() || s.contains('"'))
    {
        return None;
    }
    let mut out = String::new();
    if let Some(cc) = country {
        out.push_str(cc);
        out.push(' ');
    }
    out.push_str(number.strip_suffix(' ').filter(|s| !s.is_empty()).unwrap_or(number));
    if let Some(ext) = extension {
        out.push(' ');
        out.push_str(ext);
    }
    Some(out)
}
