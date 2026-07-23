APP_NAME    := webdesk
BUILD_DIR   := build/bin
GO_TAGS     := gtk3
GO          := go
WAILS       := wails3

.PHONY: linux windows dev clean icons generate help

help:
	@echo "WebDesk Makefile"
	@echo ""
	@echo "  make linux      Build for Linux (GTK3)"
	@echo "  make windows    Build for Windows"
	@echo "  make dev        Run development server"
	@echo "  make clean      Remove build artifacts"
	@echo "  make icons      Generate all icon variants"
	@echo "  make generate   Regenerate Wails JS bindings"

linux:
	$(WAILS) build -tags $(GO_TAGS)
	@echo "Built: $(BUILD_DIR)/$(APP_NAME)"

windows:
	GOOS=windows GOARCH=amd64 $(WAILS) build
	@echo "Built: $(BUILD_DIR)/$(APP_NAME).exe"

dev:
	$(WAILS) dev -tags $(GO_TAGS)

clean:
	rm -rf $(BUILD_DIR)

icons:
	python3 -c "\
import cairosvg, os;\
d = '$(BUILD_DIR)';\
os.makedirs(d, exist_ok=True);\
icons = {\
  'webdesk-dark': '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 512 512\"><rect width=\"512\" height=\"512\" rx=\"108\" fill=\"#1a1a2e\"/><rect x=\"76\" y=\"100\" width=\"160\" height=\"130\" rx=\"14\" fill=\"#e0e0e0\" opacity=\"0.9\"/><rect x=\"256\" y=\"100\" width=\"180\" height=\"80\" rx=\"14\" fill=\"#e0e0e0\" opacity=\"0.5\"/><rect x=\"76\" y=\"250\" width=\"360\" height=\"50\" rx=\"12\" fill=\"#e0e0e0\" opacity=\"0.3\"/><rect x=\"76\" y=\"320\" width=\"360\" height=\"80\" rx=\"14\" fill=\"#e0e0e0\" opacity=\"0.2\"/><circle cx=\"156\" cy=\"410\" r=\"20\" fill=\"#818cf8\"/><circle cx=\"220\" cy=\"410\" r=\"20\" fill=\"#22d3ee\"/><circle cx=\"284\" cy=\"410\" r=\"20\" fill=\"#fb7185\"/></svg>',\
  'webdesk-minimal': '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 512 512\"><rect width=\"512\" height=\"512\" rx=\"108\" fill=\"#ffffff\"/><rect x=\"106\" y=\"130\" width=\"130\" height=\"130\" rx=\"16\" fill=\"#1a1a2e\"/><rect x=\"256\" y=\"130\" width=\"150\" height=\"70\" rx=\"16\" fill=\"#1a1a2e\" opacity=\"0.4\"/><rect x=\"256\" y=\"220\" width=\"70\" height=\"40\" rx=\"10\" fill=\"#1a1a2e\" opacity=\"0.3\"/><rect x=\"340\" y=\"220\" width=\"66\" height=\"40\" rx=\"10\" fill=\"#1a1a2e\" opacity=\"0.3\"/><rect x=\"106\" y=\"280\" width=\"300\" height=\"10\" rx=\"5\" fill=\"#1a1a2e\" opacity=\"0.15\"/><rect x=\"106\" y=\"310\" width=\"300\" height=\"70\" rx=\"14\" fill=\"#1a1a2e\" opacity=\"0.08\"/></svg>',\
  'webdesk-gradient': '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 512 512\"><defs><linearGradient id=\"bg\" x1=\"0\" y1=\"0\" x2=\"1\" y2=\"1\"><stop offset=\"0%\" stop-color=\"#667eea\"/><stop offset=\"100%\" stop-color=\"#764ba2\"/></linearGradient></defs><rect width=\"512\" height=\"512\" rx=\"108\" fill=\"url(#bg)\"/><rect x=\"76\" y=\"110\" width=\"160\" height=\"120\" rx=\"14\" fill=\"#fff\" opacity=\"0.9\"/><rect x=\"256\" y=\"110\" width=\"180\" height=\"70\" rx=\"14\" fill=\"#fff\" opacity=\"0.5\"/><rect x=\"256\" y=\"200\" width=\"80\" height=\"40\" rx=\"10\" fill=\"#fff\" opacity=\"0.35\"/><rect x=\"352\" y=\"200\" width=\"84\" height=\"40\" rx=\"10\" fill=\"#fff\" opacity=\"0.35\"/><rect x=\"76\" y=\"250\" width=\"360\" height=\"10\" rx=\"5\" fill=\"#fff\" opacity=\"0.2\"/><rect x=\"76\" y=\"280\" width=\"360\" height=\"100\" rx=\"14\" fill=\"#fff\" opacity=\"0.15\"/></svg>',\
  'webdesk-terminal': '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 512 512\"><rect width=\"512\" height=\"512\" rx=\"108\" fill=\"#0d1117\"/><rect x=\"60\" y=\"70\" width=\"392\" height=\"300\" rx=\"18\" fill=\"#161b22\" stroke=\"#30363d\" stroke-width=\"2\"/><circle cx=\"96\" cy=\"96\" r=\"8\" fill=\"#ff6b6b\"/><circle cx=\"120\" cy=\"96\" r=\"8\" fill=\"#ffd43b\"/><circle cx=\"144\" cy=\"96\" r=\"8\" fill=\"#51cf66\"/><text x=\"86\" y=\"152\" font-family=\"monospace\" font-size=\"22\" fill=\"#58a6ff\">&gt;_ webdesk</text><rect x=\"86\" y=\"178\" width=\"200\" height=\"4\" rx=\"2\" fill=\"#58a6ff\" opacity=\"0.5\"/><rect x=\"86\" y=\"196\" width=\"140\" height=\"4\" rx=\"2\" fill=\"#8b949e\" opacity=\"0.4\"/><rect x=\"86\" y=\"214\" width=\"280\" height=\"4\" rx=\"2\" fill=\"#8b949e\" opacity=\"0.3\"/><rect x=\"86\" y=\"232\" width=\"100\" height=\"4\" rx=\"2\" fill=\"#58a6ff\" opacity=\"0.5\"/><rect x=\"60\" y=\"400\" width=\"392\" height=\"60\" rx=\"14\" fill=\"#161b22\" stroke=\"#30363d\" stroke-width=\"1\" opacity=\"0.6\"/></svg>',\
  'webdesk-ocean': '<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 512 512\"><defs><linearGradient id=\"sea\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\"><stop offset=\"0%\" stop-color=\"#0f4c75\"/><stop offset=\"100%\" stop-color=\"#1b262c\"/></linearGradient></defs><rect width=\"512\" height=\"512\" rx=\"108\" fill=\"url(#sea)\"/><rect x=\"76\" y=\"100\" width=\"160\" height=\"120\" rx=\"14\" fill=\"#bbe1fa\" opacity=\"0.85\"/><rect x=\"256\" y=\"100\" width=\"180\" height=\"70\" rx=\"14\" fill=\"#bbe1fa\" opacity=\"0.45\"/><rect x=\"256\" y=\"190\" width=\"80\" height=\"40\" rx=\"10\" fill=\"#bbe1fa\" opacity=\"0.3\"/><rect x=\"352\" y=\"190\" width=\"84\" height=\"40\" rx=\"10\" fill=\"#bbe1fa\" opacity=\"0.3\"/><rect x=\"76\" y=\"240\" width=\"360\" height=\"10\" rx=\"5\" fill=\"#3282b8\" opacity=\"0.4\"/><rect x=\"76\" y=\"270\" width=\"360\" height=\"120\" rx=\"14\" fill=\"#bbe1fa\" opacity=\"0.12\"/><circle cx=\"400\" cy=\"420\" r=\"30\" fill=\"#fbbf24\" opacity=\"0.7\"/></svg>'\
};\
[cairosvg.svg2png(bytestring=s.encode(), write_to=os.path.join(d, n+'.png'), output_width=512, output_height=512) or print(f'  {n}.png') for n,s in icons.items()]"
	@echo "Icons generated in $(BUILD_DIR)/"

generate:
	$(WAILS) generate bindings
	@cp -f frontend/bindings/webdesk/siteservice.js frontend/dist/bindings/webdesk/siteservice.js
	@cp -f frontend/bindings/webdesk/models.js frontend/dist/bindings/webdesk/models.js
	@cp -f frontend/bindings/webdesk/index.js frontend/dist/bindings/webdesk/index.js
	@echo "Bindings regenerated and copied to frontend/dist/"
