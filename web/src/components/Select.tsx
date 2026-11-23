import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";

type Option = { value: string; label: string };

export function Select({ value, options, onChange, label, ariaLabel, disabled = false }: {
  value: string;
  options: Option[];
  onChange: (value: string) => void;
  label?: string;
  ariaLabel: string;
  disabled?: boolean;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const search = useRef({ text: "", at: 0 });
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [position, setPosition] = useState<{ left: number; top: number; width: number; maxHeight: number } | null>(null);
  const selected = options.findIndex((option) => option.value === value);

  function expand(index = Math.max(0, selected)) {
    setActive(index);
    search.current = { text: "", at: 0 };
    setOpen(true);
  }
  function choose(index: number) {
    const option = options[index];
    if (!option) return;
    setOpen(false);
    if (option.value !== value) onChange(option.value);
    trigger.current?.focus();
  }

  useLayoutEffect(() => {
    if (!open) return;
    function place() {
      const rect = trigger.current?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(Math.max(rect.width, 180), window.innerWidth - 16);
      const desired = Math.min(options.length * 34 + 8, 280);
      const below = window.innerHeight - rect.bottom - 12;
      const above = rect.top - 12;
      const down = below >= Math.min(desired, 180) || below >= above;
      const maxHeight = Math.max(0, Math.min(desired, down ? below : above));
      setPosition({
        width,
        left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)),
        top: down ? rect.bottom + 4 : rect.top - maxHeight - 4,
        maxHeight,
      });
    }
    function scroll(event: Event) {
      if (event.target instanceof Node && menu.current?.contains(event.target)) return;
      setOpen(false);
    }
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", scroll, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", scroll, true);
    };
  }, [open, options.length]);

  useEffect(() => {
    if (!open) return;
    function outside(event: PointerEvent) {
      if (event.target instanceof Node && !trigger.current?.contains(event.target) && !menu.current?.contains(event.target)) setOpen(false);
    }
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);

  useLayoutEffect(() => {
    if (!open || !menu.current) return;
    const option = menu.current.children.item(active) as HTMLElement | null;
    if (!option) return;
    const list = menu.current;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    else if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight) {
      list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
    }
  }, [active, open, position]);

  function keyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.nativeEvent.isComposing || !options.length) return;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      if (event.key === "Home") expand(0);
      else if (event.key === "End") expand(options.length - 1);
      else if (!open) expand();
      else setActive((index) => (index + (event.key === "ArrowDown" ? 1 : -1) + options.length) % options.length);
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (open) choose(active);
      else expand();
    } else if (event.key === "Escape" && open) {
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
    } else if (event.key === "Tab") setOpen(false);
    else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault();
      const now = Date.now();
      const text = (now - search.current.at < 600 ? search.current.text : "") + event.key.toLocaleLowerCase();
      search.current = { text, at: now };
      const index = options.findIndex((option) => option.label.toLocaleLowerCase().startsWith(text));
      if (index >= 0) {
        setActive(index);
        setOpen(true);
      }
    }
  }

  return <>
    <button ref={trigger} type="button" role="combobox" className="ui-select" disabled={disabled || !options.length}
      aria-label={ariaLabel} aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? id : undefined}
      aria-activedescendant={open ? `${id}-${active}` : undefined}
      onClick={() => open ? setOpen(false) : expand()} onKeyDown={keyDown} onBlur={() => setOpen(false)}>
      {label && <span className="ui-select-label">{label}：</span>}
      <span className="ui-select-value">{options[selected]?.label || "请选择"}</span>
      <ChevronDown size={15} aria-hidden="true" />
    </button>
    {open && createPortal(
      <div ref={menu} id={id} role="listbox" aria-label={ariaLabel} className="ui-select-menu"
        style={{ ...position, visibility: position ? "visible" : "hidden" }}>
        {options.map((option, index) => <div key={option.value} id={`${id}-${index}`} role="option"
          aria-selected={option.value === value} className={`ui-select-option ${active === index ? "active" : ""}`}
          onPointerMove={() => setActive(index)} onPointerDown={(event) => event.preventDefault()} onClick={() => choose(index)}>
          <span>{option.label}</span>{option.value === value && <Check size={15} aria-hidden="true" />}
        </div>)}
      </div>, trigger.current?.closest("dialog") ?? document.body,
    )}
  </>;
}
