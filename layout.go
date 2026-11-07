package main

type verticalLayout struct {
	progressTop, pathTop, logTop, logHeight, pathHeight int32
}

// Keep scroll calculations and rendered positions on the same geometry.
func calculateVerticalLayout(height int32) verticalLayout {
	logHeight := max(height/3, int32(190))
	logHeight = max(int32(140), min(logHeight, height-220))
	const progressTop int32 = 58
	const pathTop int32 = progressTop + 12 + 12
	logTop := height - 12 - logHeight
	return verticalLayout{progressTop: progressTop, pathTop: pathTop, logTop: logTop, logHeight: logHeight, pathHeight: max(int32(0), logTop-pathTop-12)}
}
