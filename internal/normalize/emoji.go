package normalize

// spamEmoji maps emoji common in spam (money, hype, giveaways) to feature
// tokens (DESIGN.md §7.2 step 8). Skin tones and variation selectors are
// separate code points, so they never change the token.
var spamEmoji = map[rune]string{
	'💰': "money_bag",
	'💸': "money_wings",
	'💵': "dollar_banknote",
	'💶': "euro_banknote",
	'💷': "pound_banknote",
	'💴': "yen_banknote",
	'💲': "heavy_dollar",
	'🤑': "money_mouth",
	'🪙': "coin",
	'🔥': "fire",
	'📈': "chart_up",
	'💹': "chart_yen",
	'🚀': "rocket",
	'💎': "gem",
	'🎁': "gift",
	'🎰': "slot_machine",
	'🏦': "bank",
}

// EmojiTokens returns one token per spam emoji in s, in order, or nil.
func EmojiTokens(s string) []string {
	var toks []string
	for _, r := range s {
		if r < 0x1F000 {
			continue
		}
		if t, ok := spamEmoji[r]; ok {
			toks = append(toks, t)
		}
	}
	return toks
}
