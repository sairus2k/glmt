# glmt demo recording
# Prerequisites:
#   brew install vhs
#   go build -o glmt ./cmd/glmt/
# Usage:
#   vhs demo.tape

Output demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 700
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 50ms
Set WindowBar Colorful
Set WindowBarSize 40

# Start glmt in demo mode
Type "./glmt --demo"
Enter
Sleep 1.5s

# RepoPicker: type to filter, select backend-api
Type "back"
Sleep 800ms
Enter
Sleep 1.5s

# MRList: select all eligible MRs
Type "a"
Sleep 2s

# Start the merge train
Enter
Sleep 12s

# Show the finished state
Sleep 2s

# Quit
Type "q"
Sleep 500ms
