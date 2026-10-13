import { Pressable, View, type StyleProp, type ViewStyle } from "react-native";

import { radius, space, useStyles } from "@/ui/theme";

interface Props {
  children: React.ReactNode;
  onPress?: () => void;
  style?: StyleProp<ViewStyle>;
  testID?: string;
  accessibilityLabel?: string;
}

export function Card({ children, onPress, style, testID, accessibilityLabel }: Props) {
  const s = useStyles((c) => ({
    card: { backgroundColor: c.card, borderColor: c.line, borderWidth: 1, borderRadius: radius.lg, padding: space.lg },
    pressed: { backgroundColor: c.elevated },
  }));
  if (!onPress) {
    return (
      <View testID={testID} style={[s.card, style]}>
        {children}
      </View>
    );
  }
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      accessibilityLabel={accessibilityLabel}
      onPress={onPress}
      style={({ pressed }) => [s.card, pressed && s.pressed, style]}
    >
      {children}
    </Pressable>
  );
}
