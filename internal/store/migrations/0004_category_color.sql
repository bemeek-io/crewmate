-- Categories are identified by a color swatch rather than an emoji.
ALTER TABLE categories DROP COLUMN emoji;
UPDATE categories SET color = '' WHERE color IS NULL;
