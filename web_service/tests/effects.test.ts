// React calls whatever an effect returns as its cleanup ("i is not a function" in production when it is a promise or
// a value): every effect's arrow body is a block or a function, and every `return` in one returns a function.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const root = join(__dirname, "..");
const files = (dir: string): string[] =>
  readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) return files(p);
    return /\.tsx?$/.test(f) ? [p] : [];
  });

/** A returned expression that is certainly a cleanup function, undefined, or the call of a follow…() (an unsubscribe). */
const fine = (e: ts.Expression): boolean =>
  ts.isArrowFunction(e) ||
  ts.isFunctionExpression(e) ||
  ts.isVoidExpression(e) ||
  (ts.isIdentifier(e) && e.text === "undefined") ||
  (ts.isCallExpression(e) && /^follow[A-Z]/.test(e.expression.getText())) ||
  (ts.isParenthesizedExpression(e) && fine(e.expression)) ||
  (ts.isConditionalExpression(e) && fine(e.whenTrue) && fine(e.whenFalse));

describe("effects", () => {
  it("never return a value that is not a cleanup function", () => {
    const bad: string[] = [];
    for (const f of ["components", "app", "lib"].flatMap((d) => files(join(root, d)))) {
      const sf = ts.createSourceFile(f, readFileSync(f, "utf8"), ts.ScriptTarget.Latest, true);
      const at = (n: ts.Node) =>
        `${f.slice(root.length + 1)}:${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1}`;
      const visit = (n: ts.Node): void => {
        const fn =
          ts.isCallExpression(n) &&
          /(^|\.)use(Layout|Insertion)?Effect$/.test(n.expression.getText())
            ? n.arguments[0]
            : undefined;
        if (fn && (ts.isArrowFunction(fn) || ts.isFunctionExpression(fn))) {
          if (!ts.isBlock(fn.body)) {
            if (!fine(fn.body)) bad.push(at(fn.body));
          } else {
            const ret = (m: ts.Node): void => {
              if (ts.isFunctionLike(m)) return;
              if (ts.isReturnStatement(m) && m.expression && !fine(m.expression)) bad.push(at(m));
              ts.forEachChild(m, ret);
            };
            ts.forEachChild(fn.body, ret);
          }
        }
        ts.forEachChild(n, visit);
      };
      visit(sf);
    }
    expect(bad).toEqual([]);
  });
});
