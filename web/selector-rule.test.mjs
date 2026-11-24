import assert from "node:assert/strict";
import { test } from "node:test";
import {
  analyzeSelector,
  parseSelector,
  selectorSemantics,
} from "./src/selector-rule.mjs";

function lines(description) {
  return [description.text, ...(description.children ?? []).flatMap(lines)];
}

test("explains simple selectors in a single sentence", () => {
  for (const [source, statement] of [
    [".btn", "class 包含 btn 的元素"],
    ["#submit", "ID 为 submit 的元素"],
    ["div.card.active", "同时包含 card 和 active 两个 class 的 div 元素"],
    ['input[type="text"]', "type 属性为 text 的 input 元素"],
  ]) {
    const result = analyzeSelector(source);
    assert.equal(result.error, null);
    assert.deepEqual(result.description, { text: statement });
  }
});

test("distinguishes child, descendant and both sibling relationships", () => {
  for (const [operator, relation, wording] of [
    [">", "child", "直接子元素"],
    [" ", "descendant", "后代元素"],
    ["+", "adjacent", "紧接着"],
    ["~", "sibling", "同级兄弟元素"],
  ]) {
    const source = `.header ${operator} .btn`;
    assert.equal(
      selectorSemantics(parseSelector(source))[0].steps[1].relation,
      relation,
    );
    assert.ok(analyzeSelector(source).description.text.includes(wording));
  }
});

test("renders conjunctions, alternatives and negations at the proper level", () => {
  const conjunction = analyzeSelector(
    ".card > .btn:not(.disabled)",
  ).description;
  assert.deepEqual(conjunction, {
    text: "匹配 class 包含 card 的元素下，满足以下全部条件的直接子元素",
    children: [{ text: "class 包含 btn" }, { text: "class 不包含 disabled" }],
  });
  assert.equal(analyzeSelector(".btn, #submit").description.children.length, 2);
  assert.match(
    analyzeSelector(".btn:not(.disabled)").description.text,
    /且.*不包含 disabled/,
  );

  const nested = analyzeSelector(
    "div:has(> :is(img, svg):not([hidden], .excluded))",
  );
  const details = lines(nested.description).join("\n");
  assert.equal(nested.error, null);
  assert.deepEqual(nested.warnings, []);
  assert.match(details, /直接子元素/);
  assert.match(details, /任意一组条件/);
  assert.match(details, /不匹配以下任何一组条件/);
  assert.match(details, /具有 hidden 属性/);
  assert.match(
    analyzeSelector("div:has(> img)").description.text,
    /包含 img 直接子元素/,
  );
});

test("uses decoded CSS escapes and distinguishes attribute operators and flags", () => {
  const escaped = analyzeSelector(
    String.raw`.btn\:active#\31 23[data-title="A\20 B"]`,
  );
  assert.match(escaped.description.text, /btn:active.*ID 为 123.*A B/);
  assert.equal(escaped.error, null);
  for (const [operator, words] of [
    ["=", "为"],
    ["~=", "空格分隔值包含"],
    ["|=", "或以"],
    ["^=", "开头"],
    ["$=", "结尾"],
    ["*=", "包含"],
  ]) {
    const description = analyzeSelector(`[data-role${operator}"hello" i]`)
      .description.text;
    assert.ok(description.includes(words));
    assert.ok(description.includes("忽略 ASCII 字母大小写"));
  }
  assert.match(
    analyzeSelector("[data-role=hello s]").description.text,
    /区分大小写/,
  );
  assert.match(
    analyzeSelector('[title=""], [hidden]').description.children[0].text,
    /空字符串/,
  );
});

test("explains pseudos and preserves conditions outside its explanation coverage", () => {
  assert.match(analyzeSelector("a:focus-visible").description.text, /具有焦点/);
  assert.match(
    analyzeSelector("li:nth-child(2n+1)").description.text,
    /从前向后计数/,
  );
  const filteredPosition = analyzeSelector("li:nth-child(2n+1 of .selected)");
  assert.match(
    filteredPosition.description.text,
    /保留原始条件 :nth-child\(2n\+1 of \.selected\)/,
  );
  assert.match(filteredPosition.warnings[0], /暂无法准确说明/);
  const pseudo = analyzeSelector(".btn::before");
  assert.match(pseudo.description.text, /::before 伪元素/);
  assert.match(pseudo.warnings[0], /无法把它作为独立的点击目标/);
  assert.match(analyzeSelector('input[type="text"]').warnings[0], /SDK 会跳过/);
  assert.deepEqual(analyzeSelector(".btn:has(input[type=text])").warnings, []);
  assert.match(analyzeSelector("col || td").warnings[0], /col \|\| td/);
});

test("reports parser errors and browser support errors without losing valid input text", () => {
  for (const source of [
    "a,",
    "div >",
    "#",
    "a..b",
    "[data-mode=]",
    ":has(>)",
    ":not()",
    "[x=y z]",
  ]) {
    const result = analyzeSelector(source);
    assert.equal(result.description, null, source);
    assert.match(result.error, /选择器语法错误/);
  }
  assert.deepEqual(analyzeSelector(" "), {
    description: null,
    error: null,
    warnings: [],
  });
  const unsupported = analyzeSelector("  col || td  ", (source) => {
    assert.equal(source, "  col || td  ");
    throw new SyntaxError("不支持列组合符");
  });
  assert.match(unsupported.error, /当前浏览器无法匹配.*不支持列组合符/);
  assert.match(unsupported.description.text, /col \|\| td/);

  const forgivingInput = ":is(.btn, .invalid >)";
  const forgiving = analyzeSelector(forgivingInput, (source) => {
    assert.equal(source, forgivingInput);
  });
  assert.equal(forgiving.error, null);
  assert.ok(forgiving.description.text.includes(forgivingInput));
  assert.match(forgiving.warnings[0], /暂无法准确说明/);
});
