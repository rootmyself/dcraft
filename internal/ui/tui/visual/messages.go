package visual

// Korean user-facing text owned by the visual layer: frame labels, navigation
// words and the document renderer's fixed vocabulary. This is a relocation, not
// a rewording: every string is byte-identical to what the renderer drew before.
const (
	// Navigation. The title comes from the TUI; the label and hint here belong
	// to the pane the renderer draws.
	msgNavClose      = "esc 닫기"
	msgNavExpand     = "tab 펼치기"
	msgNavCollapse   = "tab 접기"
	msgNavMenuLabel  = "메뉴"
	msgNavFeatureCnt = "%d개 항목"
	msgNavSectionCnt = "%s %d"

	// The danger badge on a navigation entry. The word is a text channel, so
	// the risk never depends on color.
	msgDangerBadge = " 위험"

	// Document renderer vocabulary.
	msgEmptyTable = "표시할 항목이 없음."
	// Badge fallbacks: the approved shortening cascade when a state label does
	// not fit its column.
	msgBadgeNotCreated = " 미생성"
	msgBadgeRunning    = " 실행"
	msgBadgeStopped    = " 중지"
	msgBadgeUnhealthy  = " 비정상"
	msgBadgeStarting   = " 확인"
	msgBadgeRisk       = " 위험"

	// Callout severity words. Every callout carries one, so severity never
	// depends on color.
	msgCalloutWarn   = "주의"
	msgCalloutDanger = "위험"
	msgCalloutNote   = "참고"
	msgCalloutInfo   = "안내"

	// The continuation heading of a modal risk band that had to be cut.
	msgRiskContinuation = "위험 · 이어서"

	// msgButtonFocus marks the button Enter will press. The marker is the
	// non-color channel: a colorless terminal still shows which answer is
	// selected.
	msgButtonFocus = "▸ "

	// The scroll hint states what is off screen and always names the keys.
	msgHintBoth  = "↑ %d개 · ↓ %d개 더 있음 · PgUp/PgDn"
	msgHintAbove = "↑ %d개 더 있음 · PgUp/PgDn"
	msgHintBelow = "↓ %d개 더 있음 · PgUp/PgDn"
)

// Footer keycap labels. They are drawn by the shared keycap row, so they live
// with the renderer rather than with a screen.
const (
	msgKeyRun         = "실행"
	msgKeyCancel      = "취소"
	msgKeyItem        = "항목"
	msgKeyButton      = "버튼"
	msgKeyHelp        = "도움말"
	msgKeySubmit      = "제출"
	msgKeyNextField   = "다음 항목"
	msgKeyPrev        = "이전"
	msgKeyCloseScreen = "화면 닫기"
	msgKeyStop        = "중단"
	msgKeyDetach      = "접속 종료"
	msgKeySafeCancel  = "안전 취소"
	msgKeyQuitBlocked = "종료 불가"
	msgKeyMove        = "이동"
	msgKeyQuit        = "종료"
	msgKeyFind        = "찾기"
	msgKeyMenu        = "메뉴"
	msgKeyRefresh     = "새로고침"
)
