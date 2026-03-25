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
Wait+Screen /filter/

# RepoPicker: type to filter, select backend-api
Sleep 500ms
Type "back"
Sleep 800ms
Enter
Wait+Screen /toggle/

# MRList: select all eligible MRs
Sleep 500ms
Type "a"
Sleep 2s

# Start the merge train
Enter
Wait+Screen /Finished/

# Show the finished state
Sleep 3s

# Quit
Type "q"
Sleep 500ms
