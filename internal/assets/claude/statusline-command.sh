#!/bin/zsh
setopt extendedglob
# Compact, responsive status line for Claude Code:
# one line when it fits the terminal, two aligned rows when it doesn't.
# Installed by brunnr-code; the version below is baked in at install/sync time.
input=$(cat)

IFS=$'\t' read -r model effort ctx five five_r week week_r dir style added removed dur <<< "$(echo "$input" | jq -r '[
  (.model.display_name // "-"),
  (.effort.level // "-"),
  (.context_window.used_percentage // "-"),
  (.rate_limits.five_hour.used_percentage // "-"),
  (.rate_limits.five_hour.resets_at // "-"),
  (.rate_limits.seven_day.used_percentage // "-"),
  (.rate_limits.seven_day.resets_at // "-"),
  (.workspace.current_dir // .cwd // "-"),
  (.output_style.name // "-"),
  (.cost.total_lines_added // 0),
  (.cost.total_lines_removed // 0),
  (.cost.total_duration_ms // 0)
] | map(tostring) | join("\t")')"

E=$'\e'; RST="${E}[0m"; DIM="${E}[38;5;240m"; SEP="${DIM} │ ${RST}"
BARW=5

color() { # pct -> 256 color code
  local p=${1%.*}
  if (( p >= 80 )); then echo 203; elif (( p >= 50 )); then echo 221; else echo 114; fi
}

bar() { # pct width
  local p=${1%.*} w=${2:-$BARW} f c i out=""
  (( p > 100 )) && p=100
  f=$(( (p * w + 50) / 100 ))
  (( p > 0 && f == 0 )) && f=1
  c=$(color "$p")
  out="${E}[38;5;${c}m"
  for ((i=0;i<w;i++)); do
    (( i == f )) && out+="${DIM}"
    if (( i < f )); then out+="█"; else out+="░"; fi
  done
  echo "${out}${RST}"
}

epoch_fmt() { # epoch format: BSD/macOS date -r, then GNU date -d @
  date -r "$1" "$2" 2>/dev/null || date -d "@$1" "$2" 2>/dev/null
}

seg() { # label pct reset_text
  local p=${2%.*} c
  c=$(color "$p")
  printf '%s%s%s%s %s%d%%%s' "${DIM}" "$1" "$RST" "$(bar "$p")" "${E}[38;5;${c}m" "$p" "$RST"
  [[ -n "$3" ]] && printf '%s↻%s%s' "$DIM" "$3" "$RST"
}

# ── Identity pieces ──
a1="${E}[38;5;111m${model}${RST}"
[[ "$effort" != "-" ]] && a1+=" ${E}[38;5;176m${effort}${RST}"
[[ "$style" != "-" ]] && a1+=" ${DIM}${style}${RST}"
a1+=" ${DIM}brunnr-code __BRUNNR_CODE_VERSION__${RST}"

a2=""
[[ "$dir" != "-" ]] && a2="${E}[38;5;117m${dir:t}${RST}"
if [[ "$dir" != "-" ]] && git -C "$dir" --no-optional-locks rev-parse --git-dir >/dev/null 2>&1; then
  br=$(git -C "$dir" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null || git -C "$dir" --no-optional-locks rev-parse --short HEAD 2>/dev/null)
  a2+=" ${E}[38;5;179m⎇${br}${RST}"
  read -r behind ahead <<< "$(git -C "$dir" --no-optional-locks rev-list --left-right --count '@{u}...HEAD' 2>/dev/null)"
  [[ -n "$ahead" && "$ahead" -gt 0 ]] && a2+="${E}[38;5;114m↑${ahead}${RST}"
  [[ -n "$behind" && "$behind" -gt 0 ]] && a2+="${E}[38;5;203m↓${behind}${RST}"
  n=$(git -C "$dir" --no-optional-locks status --porcelain 2>/dev/null | wc -l | tr -d ' ')
  (( n > 0 )) && a2+="${E}[38;5;221m●${n}${RST}"
fi

a3=""
(( added > 0 || removed > 0 )) && a3="${E}[38;5;114m+${added}${E}[38;5;203m−${removed}${RST}"
s_=$(( ${dur%.*} / 1000 ))
if (( s_ >= 60 )); then
  if (( s_ >= 3600 )); then t="$((s_/3600))h$(( (s_%3600)/60 ))m"; else t="$((s_/60))m"; fi
  [[ -n "$a3" ]] && a3+=" "
  a3+="${DIM}⏱${t}${RST}"
fi

# ── Usage pieces ──
b1="" b2="" b3=""
if [[ "$ctx" != "-" ]]; then
  if (( ${ctx%.*} >= 80 )); then
    b1="${E}[1;5;38;5;203m⚠${RST}$(seg ' ' "$ctx")"
  else
    b1="$(seg 'ctx ' "$ctx")"
  fi
fi
if [[ "$five" != "-" ]]; then
  r=""; [[ "$five_r" != "-" ]] && r=$(epoch_fmt "${five_r%.*}" +%H:%M)
  b2="$(seg '5h ' "$five" "$r")"
fi
if [[ "$week" != "-" ]]; then
  r=""; [[ "$week_r" != "-" ]] && r=$(epoch_fmt "${week_r%.*}" "+%d/%m %H:%M")
  b3="$(seg '7d ' "$week" "$r")"
fi

ANSI=$'\e'"\\[[0-9;]#m"
vis() { local t=${1//${~ANSI}/}; echo ${(m)#t}; }
pad() { local w=$(vis "$1"); printf '%s%*s' "$1" $(( $2 - w )) ''; }
join_sep() { local o="" p; for p in "$@"; do [[ -n "$p" ]] && o+="${o:+$SEP}$p"; done; printf '%s' "$o"; }

# ── Terminal width (0 = unknown → stay on one line) ──
cols=$( { stty size </dev/tty; } 2>/dev/null | cut -d' ' -f2)
[[ -z "$cols" || "$cols" == 0 ]] && cols=${COLUMNS:-0}

line=$(join_sep "$a1" "$b1" "$b2" "$b3" "$a2" "$a3")
if (( cols == 0 || $(vis "$line") <= cols - 4 )) || [[ -z "$b1$b2$b3" ]]; then
  printf '%s' "$line"
  exit 0
fi

# Too wide: two rows with aligned columns
cols_=()
for i in 1 2 3; do [[ -n ${(P)${:-a$i}}${(P)${:-b$i}} ]] && cols_+=$i; done
row1="" row2=""
for i in $cols_; do
  x=${(P)${:-a$i}} y=${(P)${:-b$i}}
  w1=$(vis "$x") w2=$(vis "$y"); (( w = w1 > w2 ? w1 : w2 ))
  [[ -n $row1$row2 ]] && { row1+="$SEP"; row2+="$SEP"; }
  if [[ $i != ${cols_[-1]} ]]; then row1+=$(pad "$x" $w); row2+=$(pad "$y" $w); else row1+="$x"; row2+="$y"; fi
done
printf '%s\n%s' "$row1" "$row2"
