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

use super::{is_key, rest_is_po, s};
use crate::tag::Field;

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let mut out = String::new();

    if is_key(d, i, "negative") {
        if s(d, i, "negative")? != "true" {
            return None;
        }
        out.push_str("mínusz ");
        i += 1;
    }

    let int_part = if is_key(d, i, "integer_part") {
        let val = s(d, i, "integer_part")?;
        if val.is_empty() {
            return None;
        }
        i += 1;
        Some(val)
    } else {
        None
    };

    if is_key(d, i, "fractional_part") {
        let frac = s(d, i, "fractional_part")?;
        if frac.is_empty() {
            return None;
        }
        i += 1;
        if let Some(int_v) = int_part {
            out.push_str(int_v);
            out.push(' ');
        }
        out.push_str("egész ");
        out.push_str(frac);
        if is_key(d, i, "quantity") {
            let q = s(d, i, "quantity")?;
            if q.is_empty() {
                return None;
            }
            i += 1;
            out.push(' ');
            out.push_str(q);
        }
    } else if let Some(int_v) = int_part {
        out.push_str(int_v);
        if is_key(d, i, "quantity") {
            let q = s(d, i, "quantity")?;
            if q.is_empty() {
                return None;
            }
            i += 1;
            out.push(' ');
            out.push_str(q);
        }
    } else {
        return None;
    }

    if !rest_is_po(d, i) {
        return None;
    }

    Some(out)
}
