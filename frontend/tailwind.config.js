/** @type {import('tailwindcss').Config} */
export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "Inter",
          "Segoe UI Variable",
          "Segoe UI",
          "PingFang SC",
          "Microsoft YaHei UI",
          "sans-serif",
        ],
        mono: [
          "JetBrains Mono",
          "Cascadia Mono",
          "Consolas",
          "monospace",
        ],
      },
    },
  },
  plugins: [],
};
