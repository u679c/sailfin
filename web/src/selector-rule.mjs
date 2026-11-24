import selectorParser from "postcss-selector-parser";

const parser = selectorParser();
const relations = new Map([
  ["", "descendant"],
  [">", "child"],
  ["+", "adjacent"],
  ["~", "sibling"],
  ["||", "column"],
]);
const logicalPseudos = new Map([
  [":is", "any"],
  [":where", "any"],
  [":not", "none"],
  [":has", "relative"],
]);
const legacyPseudoElements = new Set([
  ":before",
  ":after",
  ":first-line",
  ":first-letter",
]);
const attributeOperators = new Set(["=", "~=", "|=", "^=", "$=", "*="]);

export function parseSelector(source) {
  const root = parser.astSync(source);
  if (root.trailingComma) throw new SyntaxError("逗号后缺少选择器");
  return root;
}

export function selectorSemantics(root) {
  return root.nodes.map((node) => chain(node));
}

function chain(selector, relative = false) {
  const steps = [];
  let relation = relative ? "descendant" : null;
  let terms = [];
  for (const node of selector.nodes) {
    if (node.type === "comment") continue;
    if (node.type === "combinator") {
      if (terms.length) steps.push({ relation, terms });
      else if (!relative || steps.length || relation !== "descendant") {
        throw new SyntaxError(`组合符「${node.value.trim()}」前缺少元素`);
      }
      relation = relations.get(node.value.trim()) ?? node.value.trim();
      terms = [];
      continue;
    }
    const term = selectorTerm(node);
    if ((term.type === "tag" || term.type === "universal") && terms.length) {
      throw new SyntaxError("标签名或通配符必须放在同一组条件的开头");
    }
    terms.push(term);
  }
  if (!terms.length) throw new SyntaxError("缺少选择器或组合符后的元素");
  steps.push({ relation, terms });
  return { steps, relative, raw: selector.toString().trim() };
}

function selectorTerm(node) {
  const raw = node.toString().trim();
  const { type, value } = node;
  if (type === "attribute") {
    if (!node.attribute || (node.operator && typeof value !== "string"))
      throw new SyntaxError(`属性选择器「${raw}」缺少属性名或匹配值`);
    const flag =
      node.raws?.insensitiveFlag?.toLowerCase() ??
      (node.insensitive === true
        ? "i"
        : node.insensitive === false
          ? "s"
          : undefined);
    if (flag && flag !== "i" && flag !== "s")
      throw new SyntaxError(`属性选择器「${raw}」的标记只能是 i 或 s`);
    if (
      !node.namespace &&
      (!node.operator || attributeOperators.has(node.operator))
    ) {
      return {
        type,
        name: node.attribute,
        operator: node.operator,
        value,
        flag,
      };
    }
  }
  if (type === "class" || type === "id") {
    if (!value)
      throw new SyntaxError(
        type === "class" ? "点号后缺少 class 名" : "# 后缺少 ID",
      );
    return { type, value };
  }
  if ((type === "tag" || type === "universal") && node.namespace === undefined)
    return { type, value };
  if (type === "pseudo") {
    const name = value.toLowerCase();
    if (name.startsWith("::") || legacyPseudoElements.has(name))
      return { type: "pseudo-element", name, raw };
    const logic = logicalPseudos.get(name);
    if (logic) {
      if (!node.nodes?.length)
        throw new SyntaxError(`「${name}」需要括号内的选择器`);
      return {
        type: "logic",
        logic,
        alternatives: node.nodes.map((child) =>
          chain(child, logic === "relative"),
        ),
      };
    }
    const hasFilter = node.nodes?.some((child) =>
      child.nodes.some(
        (part) => part.type === "tag" && part.value.toLowerCase() === "of",
      ),
    );
    return {
      type: "pseudo-class",
      name,
      raw,
      functional: Boolean(node.nodes?.length),
      hasFilter,
    };
  }
  return { type: "raw", raw };
}

const pseudoStates = {
  ":active": "正处于激活状态",
  ":checked": "已选中",
  ":disabled": "已禁用",
  ":empty": "没有子元素或文本内容",
  ":enabled": "已启用",
  ":first-child": "是父元素的第一个子元素",
  ":first-of-type": "是同标签类型的第一个兄弟元素",
  ":focus": "具有焦点",
  ":focus-visible": "具有焦点且浏览器判定应显示焦点提示",
  ":focus-within": "自身或内部元素具有焦点",
  ":hover": "鼠标正悬停",
  ":last-child": "是父元素的最后一个子元素",
  ":last-of-type": "是同标签类型的最后一个兄弟元素",
  ":only-child": "是父元素唯一的子元素",
  ":only-of-type": "是父元素中其标签类型唯一的子元素",
  ":optional": "属于非必填控件",
  ":placeholder-shown": "正在显示占位提示",
  ":required": "属于必填控件",
  ":root": "是文档根元素",
  ":target": "是当前 URL 片段指向的元素",
};
const nthStates = {
  ":nth-child": "按兄弟元素从前向后计数",
  ":nth-last-child": "按兄弟元素从后向前计数",
  ":nth-of-type": "按同标签类型的兄弟元素从前向后计数",
  ":nth-last-of-type": "按同标签类型的兄弟元素从后向前计数",
};
const pseudoElements = {
  "::before": "内容之前生成的部分",
  "::after": "内容之后生成的部分",
  "::first-line": "第一行",
  "::first-letter": "首字母",
  "::marker": "列表标记",
  "::placeholder": "占位文本",
  "::selection": "被选中的文本",
  "::backdrop": "背景层",
};
const describeAttribute = {
  "=": (value) => `为 ${value}`,
  "~=": (value) => `的空格分隔值包含 ${value}`,
  "|=": (value) => `为 ${value} 或以 ${value}- 开头`,
  "^=": (value) => `以 ${value} 开头`,
  "$=": (value) => `以 ${value} 结尾`,
  "*=": (value) => `包含 ${value}`,
};

function condition(term) {
  switch (term.type) {
    case "class":
      return { text: `class 包含 ${term.value}` };
    case "id":
      return { text: `ID 为 ${term.value}` };
    case "attribute": {
      if (!term.operator) return { text: `具有 ${term.name} 属性` };
      const { flag } = term;
      const value = term.value === "" ? "空字符串" : term.value;
      const suffix =
        flag === "i"
          ? "（忽略 ASCII 字母大小写）"
          : flag === "s"
            ? "（区分大小写）"
            : "";
      return {
        text: `${term.name} 属性${describeAttribute[term.operator](value)}${suffix}`,
      };
    }
    case "pseudo-class":
      if (!term.functional && pseudoStates[term.name])
        return { text: pseudoStates[term.name] };
      if (term.functional && !term.hasFilter && nthStates[term.name])
        return { text: `${nthStates[term.name]}，位置符合 ${term.raw}` };
      return { text: `保留原始条件 ${term.raw}` };
    case "pseudo-element": {
      const name = term.name.startsWith("::") ? term.name : `:${term.name}`;
      return {
        text: `${term.raw} 伪元素${pseudoElements[name] ? `（${pseudoElements[name]}）` : ""}`,
      };
    }
    case "logic": {
      const [first] = term.alternatives;
      const single =
        term.alternatives.length === 1 && first.steps.length === 1
          ? first.steps[0]
          : null;
      if (
        term.logic === "none" &&
        single?.terms.length === 1 &&
        single.terms[0].type === "class"
      ) {
        return { text: `class 不包含 ${single.terms[0].value}` };
      }
      if (
        term.logic === "relative" &&
        single?.terms.length === 1 &&
        single.terms[0].type === "tag"
      ) {
        const tag = single.terms[0].value;
        if (single.relation === "child")
          return { text: `包含 ${tag} 直接子元素` };
        if (single.relation === "descendant")
          return { text: `包含 ${tag} 后代元素` };
      }
      return {
        text: {
          any: "满足以下任意一组条件",
          none: "不匹配以下任何一组条件",
          relative: "从当前元素出发，存在以下任意一种关系",
        }[term.logic],
        children: term.alternatives.map(describeChain),
      };
    }
    default:
      return { text: `保留原始条件 ${term.raw}` };
  }
}

const relationText = {
  descendant: ["下，", "后代元素"],
  child: ["下，", "直接子元素"],
  adjacent: ["后，紧接着的", "兄弟元素"],
  sibling: ["后，", "同级兄弟元素"],
};

function describeStep(step, before, detailed) {
  const [first] = step.terms;
  const tag = first.type === "tag" ? first.value : "";
  const pseudo = step.terms.find((term) => term.type === "pseudo-element");
  const terms = step.terms.filter(
    (term) => !["tag", "universal", "pseudo-element"].includes(term.type),
  );
  const adjectives = terms.map(condition);
  const [join, noun] = before ? relationText[step.relation] : ["", "元素"];
  const prefix = before ? `${before}${join}` : "";
  const name = tag ? `${tag} ${noun}` : noun;
  const target = pseudo ? `${name}的 ${condition(pseudo).text}` : name;
  if (
    adjectives.some((part) => part.children) ||
    (detailed && adjectives.length > 1)
  ) {
    return {
      text: `${prefix}满足以下全部条件的${target}`,
      children: adjectives,
    };
  }
  if (terms.length > 1 && terms.every((term) => term.type === "class")) {
    return {
      text: `${prefix}同时包含 ${terms.map((term) => term.value).join(" 和 ")} ${terms.length === 2 ? "两个" : "多个"} class 的 ${target}`,
    };
  }
  const qualifications = adjectives
    .map((part) => part.text)
    .map((text, index) =>
      index > 0 &&
      adjectives[0].text.startsWith("class 包含") &&
      text.startsWith("class 不包含")
        ? text.slice("class ".length)
        : text,
    )
    .join(" 且 ");
  return {
    text: `${prefix}${qualifications ? `${qualifications} 的${tag ? " " : ""}${target}` : tag || before || pseudo ? target : "任意元素"}`,
  };
}

function describeChain({ steps, relative, raw }) {
  if (steps.some(({ relation }) => relation && !relationText[relation])) {
    return { text: `保留原始表达式 ${raw}` };
  }
  let result = relative ? { text: "当前元素" } : null;
  for (const step of steps) {
    if (result?.children) {
      const target = describeStep(step, "", true);
      const relation =
        step.relation === "adjacent"
          ? "紧邻其后的兄弟元素"
          : relationText[step.relation][1];
      result = {
        text: `匹配指定关系下的${relation}`,
        children: [
          { ...result, text: `关系起点为 ${result.text}` },
          { ...target, text: `目标条件为 ${target.text}` },
        ],
      };
    } else {
      result = describeStep(step, result?.text, steps.length > 1);
    }
  }
  return result;
}

export function describeSelectorChinese(alternatives) {
  if (alternatives.length > 1) {
    return {
      text: "匹配满足以下任意一组条件的元素",
      children: alternatives.map(describeChain),
    };
  }
  const description = describeChain(alternatives[0]);
  return description.text.startsWith("匹配")
    ? description
    : {
        ...description,
        text: `${description.children ? "匹配 " : ""}${description.text}`,
      };
}

function selectorWarnings(
  alternatives,
  warnings = new Set(),
  checkClickTarget = true,
) {
  for (const { steps, raw } of alternatives) {
    for (const [index, { relation, terms }] of steps.entries()) {
      const onClickPath =
        checkClickTarget &&
        steps
          .slice(index + 1)
          .every(
            ({ relation }) => relation === "child" || relation === "descendant",
          );
      if (relation && !relationText[relation])
        warnings.add(`暂无法准确说明组合关系，已保留原式「${raw}」。`);
      for (const term of terms) {
        if (term.type === "logic")
          selectorWarnings(
            term.alternatives,
            warnings,
            onClickPath && term.logic === "any",
          );
        if (term.type === "pseudo-element")
          warnings.add(
            `「${term.raw}」是伪元素，SDK 无法把它作为独立的点击目标。请选择实际 DOM 元素。`,
          );
        if (
          term.type === "pseudo-class" &&
          [":host", ":host-context"].includes(term.name)
        ) {
          warnings.add(
            `「${term.raw}」依赖 Shadow DOM 内的匹配上下文，SDK 的页面级监听无法按该上下文匹配。`,
          );
        }
        if (
          term.type === "raw" ||
          (term.type === "pseudo-class" &&
            (term.hasFilter ||
              !(term.functional ? nthStates : pseudoStates)[term.name]))
        ) {
          warnings.add(`暂无法准确说明「${term.raw}」，已保留原式。`);
        }
      }
      if (!onClickPath) continue;
      const tag = terms
        .find((term) => term.type === "tag")
        ?.value.toLowerCase();
      const inputType = terms
        .find(
          (term) =>
            term.type === "attribute" &&
            term.name.toLowerCase() === "type" &&
            term.operator === "=",
        )
        ?.value.toLowerCase();
      const ignored = terms.some(
        (term) =>
          term.type === "attribute" &&
          ([
            "hidden",
            "contenteditable",
            "data-sailfin-ignore",
            "data-tracelite-ignore",
          ].includes(term.name.toLowerCase()) ||
            (term.name.toLowerCase() === "aria-hidden" &&
              term.operator === "=" &&
              term.value === "true")),
      );
      if (
        ignored ||
        tag === "textarea" ||
        tag === "select" ||
        (tag === "input" &&
          inputType !== undefined &&
          !["button", "submit", "reset"].includes(inputType))
      ) {
        warnings.add(`「${raw}」包含 SDK 会跳过的元素，相关点击不会被记录。`);
      }
    }
  }
  return [...warnings];
}

export function analyzeSelector(source, validateMatch) {
  if (!source.trim()) return { description: null, error: null, warnings: [] };
  let alternatives;
  try {
    alternatives = selectorSemantics(parseSelector(source));
  } catch (error) {
    // Forgiving selector lists can be valid even when a branch cannot be interpreted.
    if (validateMatch) {
      try {
        validateMatch(source);
        return {
          description: { text: `保留原始表达式 ${source}` },
          error: null,
          warnings: ["当前浏览器接受此规则，其语义暂无法准确说明。"],
        };
      } catch {
        // Prefer the structural error when both parsers reject the selector.
      }
    }
    return {
      description: null,
      error: `选择器语法错误：${error.message}`,
      warnings: [],
    };
  }
  const description = describeSelectorChinese(alternatives);
  const warnings = selectorWarnings(alternatives);
  try {
    validateMatch?.(source);
  } catch (error) {
    return {
      description,
      error: `当前浏览器无法匹配此选择器：${error.message}`,
      warnings,
    };
  }
  return { description, error: null, warnings };
}
