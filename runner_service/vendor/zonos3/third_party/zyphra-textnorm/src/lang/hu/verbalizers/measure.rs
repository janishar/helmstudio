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

use super::{cardinal, decimal, fraction};
use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut num_str = None;
    let mut units_str = None;
    let mut units_first = false;

    for (k, v) in d {
        match (k.as_ref(), v) {
            ("units", Value::Str(u)) => {
                units_str = Some(u.as_ref());
                if num_str.is_none() {
                    units_first = true;
                }
            }
            ("cardinal", Value::Dict(c)) => {
                num_str = cardinal::verbalize(c);
            }
            ("decimal", Value::Dict(dec)) => {
                num_str = decimal::verbalize(dec);
            }
            ("fraction", Value::Dict(frac)) => {
                num_str = fraction::verbalize(frac);
            }
            ("preserve_order", Value::True) => {}
            _ => return None,
        }
    }

    let n = num_str?;
    let u = units_str?;

    if units_first { Some(format!("{u} {n}")) } else { Some(format!("{n} {u}")) }
}
