/**
 * Text alignment that starts paragraphs on the language's side, whatever the native layout direction.
 *
 * Under an RTL layout (I18nManager.forceRTL) React Native swaps "left" and "right" for text: iOS in
 * RCTAttributedTextUtils.mm, Android in TextLayoutManager.getTextAlignment. With no textAlign at all,
 * Android aligns by the paragraph's layout direction and iOS by the text's script, so an Arabic heading
 * sits on the left whenever the layout flag lags the chosen language (it only applies after a reload,
 * and applyDirection gives up after one) and Latin text such as a team name sits on the left in an
 * Arabic layout. Setting the alignment explicitly, pre-swapped for the layout, puts it on the right side.
 */
export function startAlign(languageRTL: boolean, layoutRTL: boolean): "left" | "right" {
  return languageRTL !== layoutRTL ? "right" : "left";
}

export function writingDirection(languageRTL: boolean): "rtl" | "ltr" {
  return languageRTL ? "rtl" : "ltr";
}
