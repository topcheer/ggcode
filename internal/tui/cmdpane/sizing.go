package cmdpane

type Placement struct {
	Direction string
	Size      int
}

const (
	maxPercentage = 30

	widthThreshold  = 80
	heightThreshold = 50
)

func DeterminePlacement(cols, rows, pixW, pixH int) Placement {

	portrait := false
	if pixW > 0 && pixH > 0 {
		portrait = pixH > pixW
	} else {
		portrait = rows > cols
	}

	if portrait && rows > heightThreshold {
		size := rows * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "bottom", Size: size}
	}
	if cols > widthThreshold {
		size := cols * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "right", Size: size}
	}
	if rows > heightThreshold {
		size := rows * maxPercentage / 100
		if size < 1 {
			size = 1
		}
		return Placement{Direction: "bottom", Size: size}
	}
	return Placement{}
}

func (p Placement) IsActive() bool {
	return p.Direction != ""
}
