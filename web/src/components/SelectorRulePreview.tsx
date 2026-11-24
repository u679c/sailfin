import type {
  analyzeSelector,
  SelectorDescription,
} from "../selector-rule.mjs";

function Conditions({ items }: { items: SelectorDescription[] }) {
  return (
    <ul>
      {items.map((item, index) => (
        <li key={index}>
          {item.text}
          {item.children && "："}
          {item.children && <Conditions items={item.children} />}
        </li>
      ))}
    </ul>
  );
}

export function SelectorRulePreview({
  analysis,
}: {
  analysis: ReturnType<typeof analyzeSelector>;
}) {
  const { description, error, warnings } = analysis;
  return (
    <div
      className="selector-rule-preview"
      id="element-selector-preview"
      aria-live="polite"
    >
      <span className="selector-rule-title">匹配规则说明</span>
      {error && <p className="selector-rule-error">{error}</p>}
      {description ? (
        <>
          <p>
            {description.text}
            {description.children ? "：" : "。"}
          </p>
          {description.children && <Conditions items={description.children} />}
        </>
      ) : (
        !error && <p>输入选择器后查看匹配条件。</p>
      )}
      {warnings.map((warning) => (
        <p key={warning} className="selector-rule-warning">
          {warning}
        </p>
      ))}
    </div>
  );
}
