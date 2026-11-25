import type { ComponentPropsWithoutRef } from "react";

type ButtonProps = ComponentPropsWithoutRef<"button"> & {
  variant?: "primary" | "soft" | "outline" | "danger";
};

export function Button({ variant = "outline", type = "button", className = "", ...props }: ButtonProps) {
  return <button {...props} type={type} className={`btn btn-${variant}${className ? ` ${className}` : ""}`} />;
}
